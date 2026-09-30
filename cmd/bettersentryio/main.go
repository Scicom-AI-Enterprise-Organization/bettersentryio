// Command bettersentryio is the whole product: one process that ingests
// heartbeats, detects absence, and alerts. PLAN.md D2a — Postgres is the only
// external dependency; nothing else gets added here.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/api"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/metrics"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/web"
)

var version = "0.1.0-dev"

func main() {
	// `bettersentryio serve --flags` is the documented form, so the subcommand is
	// stripped before flag parsing (the flag package stops at the first non-flag).
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "serve":
			args = args[1:]
		case "version":
			fmt.Println("bettersentryio", version)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (expected: serve, version)\n", args[0])
			os.Exit(2)
		}
	}

	fs := flag.NewFlagSet("bettersentryio", flag.ExitOnError)
	var (
		databaseURL   = fs.String("database-url", os.Getenv("BSIO_DATABASE_URL"), "PostgreSQL connection URL (env BSIO_DATABASE_URL)")
		listen        = fs.String("listen", envOr("BSIO_LISTEN", ":9090"), "HTTP listen address")
		baseURL       = fs.String("base-url", os.Getenv("BSIO_BASE_URL"), "public base URL, used for links in alerts")
		tickInterval  = fs.Duration("tick-interval", 15*time.Second, "detector sweep interval")
		maxConns      = fs.Int("db-max-conns", 10, "Postgres connection pool size")
		alertWebhook  = fs.String("alert-webhook", os.Getenv("BSIO_ALERT_WEBHOOK"), "webhook URL to register as the 'default' alert channel")
		alertType     = fs.String("alert-type", envOr("BSIO_ALERT_TYPE", "webhook"), "type of --alert-webhook: webhook|slack|teams")
		adminUser     = fs.String("admin-user", envOr("BSIO_ADMIN_USER", web.DefaultUser), "username for the web UI")
		adminPass     = fs.String("admin-password", os.Getenv("BSIO_ADMIN_PASSWORD"), "password for the web UI (unset: UI sign-in is disabled)")
		adminPassFile = fs.String("admin-password-file", os.Getenv("BSIO_ADMIN_PASSWORD_FILE"), "file containing the web UI password (preferred over --admin-password)")
		apiToken      = fs.String("api-token", os.Getenv("BSIO_API_TOKEN"), "operator token for the admin API (create/delete apps, mute). Unset: any ingest key is accepted, which is development-only")
		apiTokenFile  = fs.String("api-token-file", os.Getenv("BSIO_API_TOKEN_FILE"), "file containing the operator token (preferred over --api-token)")
		sessionTTL    = fs.Duration("session-ttl", 12*time.Hour, "how long a UI session stays signed in")
		tlsCert       = fs.String("tls-cert-file", os.Getenv("BSIO_TLS_CERT_FILE"), "serve HTTPS with this certificate (PEM); needs --tls-key-file. Unset: plain HTTP behind a TLS-terminating edge")
		tlsKey        = fs.String("tls-key-file", os.Getenv("BSIO_TLS_KEY_FILE"), "private key (PEM) for --tls-cert-file")
		logFormat     = fs.String("log-format", envOr("BSIO_LOG_FORMAT", "text"), "log format: text|json")
		showVersion   = fs.Bool("version", false, "print version and exit")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("bettersentryio", version)
		return
	}

	log := newLogger(*logFormat)

	if *databaseURL == "" {
		log.Error("no database configured — pass --database-url or set BSIO_DATABASE_URL")
		os.Exit(2)
	}
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Error("--tls-cert-file and --tls-key-file go together — set both or neither")
		os.Exit(2)
	}

	// A password file beats a flag: flags are visible in `ps` to every user on the box.
	token := *apiToken
	if *apiTokenFile != "" {
		raw, err := os.ReadFile(*apiTokenFile)
		if err != nil {
			log.Error("cannot read api token file", "path", *apiTokenFile, "err", err)
			os.Exit(1)
		}
		token = strings.TrimSpace(string(raw))
	}

	password := *adminPass
	if *adminPassFile != "" {
		raw, err := os.ReadFile(*adminPassFile)
		if err != nil {
			log.Error("cannot read admin password file", "path", *adminPassFile, "err", err)
			os.Exit(2)
		}
		password = strings.TrimSpace(string(raw))
	}

	if err := run(context.Background(), log, runConfig{
		databaseURL:  *databaseURL,
		listen:       *listen,
		baseURL:      strings.TrimRight(*baseURL, "/"),
		tickInterval: *tickInterval,
		maxConns:     int32(*maxConns),
		alertWebhook: *alertWebhook,
		alertType:    *alertType,
		adminUser:    *adminUser,
		adminPass:    password,
		apiToken:     token,
		sessionTTL:   *sessionTTL,
		tlsCert:      *tlsCert,
		tlsKey:       *tlsKey,
	}); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type runConfig struct {
	databaseURL  string
	listen       string
	baseURL      string
	tickInterval time.Duration
	maxConns     int32
	alertWebhook string
	alertType    string
	adminUser    string
	adminPass    string
	apiToken     string
	sessionTTL   time.Duration
	tlsCert      string
	tlsKey       string
}

func run(parent context.Context, log *slog.Logger, cfg runConfig) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.databaseURL, cfg.maxConns)
	if err != nil {
		return err
	}
	defer db.Close()

	// A database that is not up yet is a normal startup condition. Wait for it
	// rather than crash-looping and taking the monitor down with it.
	if err := db.WaitReady(ctx, 30*time.Second); err != nil {
		return err
	}

	applied, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Info("migrations applied", "count", len(applied), "names", strings.Join(applied, ","))
	}

	boot, err := db.EnsureDefaultProject(ctx)
	if err != nil {
		return err
	}

	if cfg.alertWebhook != "" {
		blob, err := json.Marshal(map[string]string{"url": cfg.alertWebhook})
		if err != nil {
			return fmt.Errorf("encode alert channel: %w", err)
		}
		if err := db.EnsureChannel(ctx, "default", cfg.alertType, string(blob)); err != nil {
			return fmt.Errorf("register alert channel: %w", err)
		}
		log.Info("alert channel registered", "name", "default", "type", cfg.alertType)
	}

	alerter := alert.New(db, log.With("component", "alerter"), 256)
	alerter.SetBaseURL(cfg.baseURL)
	engine := monitor.NewEngine(db, alerter, log.With("component", "engine"), cfg.baseURL)
	detector := monitor.NewDetector(db, alerter, log.With("component", "detector"), cfg.tickInterval, cfg.baseURL)

	auth, err := web.NewAuth(cfg.adminUser, cfg.adminPass, cfg.sessionTTL)
	if err != nil {
		return err
	}
	if auth.Disabled {
		log.Warn("NO UI PASSWORD SET — web UI sign-in is disabled. " +
			"Set --admin-password-file or BSIO_ADMIN_PASSWORD to enable it.")
	}
	if cfg.apiToken == "" {
		log.Warn("NO API TOKEN SET — any valid ingest key may create and delete apps. " +
			"Set --api-token-file or BSIO_API_TOKEN before exposing this instance.")
	}
	ui, err := web.New(engine, auth, log.With("component", "web"))
	if err != nil {
		return err
	}
	server := api.New(db, engine, detector, alerter, log.With("component", "http"), version, cfg.apiToken, cfg.baseURL, auth)

	// Retention sweep: hourly, plus once shortly after boot so a freshly configured
	// retention does not wait an hour to take effect. The sweep holds an advisory
	// lock, so extra replicas running this loop cost one no-op query each.
	go func() {
		swept := metrics.NewCounterVec("bsio_retention_deleted_total",
			"Rows removed by the retention sweep, by kind", "kind")
		ev := events.New(db)
		retLog := log.With("component", "retention")
		sweep := func() {
			sctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			out, err := ev.SweepRetention(sctx)
			if err != nil {
				retLog.Error("sweep failed", "err", err)
				return
			}
			swept.With("events").Add(uint64(out.Events))
			swept.With("attachments").Add(uint64(out.Attachments))
			swept.With("issues").Add(uint64(out.Issues))
			if out.Events+out.Attachments+out.Issues > 0 {
				retLog.Info("swept", "events", out.Events, "attachments", out.Attachments,
					"issues", out.Issues, "more_pending", out.Truncated)
			}
		}
		select {
		case <-time.After(time.Minute):
			sweep()
		case <-ctx.Done():
			return
		}
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				sweep()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Every phase of a connection is bounded (VAPT SAST #40): without ReadTimeout a
	// client could trickle a 20 MB envelope body forever, and without IdleTimeout a
	// kept-alive connection never closed. 64 KiB of request line + headers also bounds
	// how many ?tag= / field= / search terms one query can pile into its SQL.
	httpSrv := &http.Server{
		Addr:              cfg.listen,
		Handler:           server.Handler(ui),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// Only used with --tls-cert-file. TLS 1.2 is the floor; Go's default suites
		// for 1.2 are already AEAD-only.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); alerter.Run(ctx) }()
	go func() { defer wg.Done(); detector.Run(ctx) }()

	errCh := make(chan error, 1)
	// Plain HTTP is the default because every deployment so far terminates TLS at the
	// edge (Envoy / ALB) and reaches the engine over the cluster network. VAPT SAST #50
	// flagged that as plaintext transport: --tls-cert-file closes the last hop too, for
	// installs where the network between edge and engine is not trusted.
	go func() {
		var err error
		if cfg.tlsCert != "" {
			log.Info("listening", "addr", cfg.listen, "tls", true, "version", version)
			err = httpSrv.ListenAndServeTLS(cfg.tlsCert, cfg.tlsKey)
		} else {
			log.Info("listening", "addr", cfg.listen, "tls", false, "version", version)
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	if boot.Created {
		printFirstBootHelp(cfg, boot)
	}

	select {
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	wg.Wait()
	log.Info("stopped cleanly")
	return nil
}

// printFirstBootHelp prints a copy-pasteable beat command once, on the boot that
// generated the key. Printing it on every restart would leak it into every log.
func printFirstBootHelp(cfg runConfig, boot store.Bootstrap) {
	host := cfg.baseURL
	if host == "" {
		host = "http://localhost" + cfg.listen
	}
	fmt.Fprintf(os.Stderr, `
──────────────────────────────────────────────────────────────────────
 bettersentryio is ready. Project %q created with a fresh ingest key.

   INGEST KEY   %s

 Send a heartbeat (creates the monitor on first beat):

   curl -fsS "%s/api/0/beat/tts-batcher?key=%s&every=30&progress=1"

 Monitors wall:   %s/
 Health:          %s/-/health

 This key is printed once. Store it now.
──────────────────────────────────────────────────────────────────────

`, boot.ProjectSlug, boot.PublicKey, host, boot.PublicKey, host, host)
}

func newLogger(format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if strings.EqualFold(format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
