// Package web is the operator-facing UI: server-rendered HTML, no build step,
// no JavaScript framework (PLAN D5). Templates and CSS are embedded in the binary.
package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	engine *monitor.Engine
	auth   *Auth
	log    *slog.Logger
	pages  map[string]*template.Template
	static http.Handler
}

func New(engine *monitor.Engine, auth *Auth, log *slog.Logger) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	return &Server{
		engine: engine,
		auth:   auth,
		log:    log,
		pages:  pages,
		static: http.FileServerFS(assets),
	}, nil
}

// parsePages builds one template set per page. Each page file defines "content",
// so they must not share a set or the definitions would collide.
func parsePages() (map[string]*template.Template, error) {
	names := []string{"dashboard", "monitor", "incidents", "settings"}
	out := map[string]*template.Template{}
	for _, name := range names {
		t, err := template.New(name).Funcs(funcs).ParseFS(assets,
			"templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		out[name] = t
	}
	login, err := template.New("login").Funcs(funcs).ParseFS(assets, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("parse login: %w", err)
	}
	out["login"] = login
	return out, nil
}

// Routes registers the UI on mux. Everything except /login and /static is behind
// the session check.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /login", s.getLogin)
	mux.HandleFunc("POST /login", s.postLogin)
	mux.HandleFunc("POST /logout", s.postLogout)

	protected := http.NewServeMux()
	protected.HandleFunc("GET /{$}", s.dashboard)
	protected.HandleFunc("GET /monitors/{slug}", s.monitorDetail)
	protected.HandleFunc("POST /monitors/{slug}/mute", s.mute(true))
	protected.HandleFunc("POST /monitors/{slug}/unmute", s.mute(false))
	protected.HandleFunc("GET /incidents", s.incidents)
	protected.HandleFunc("GET /settings", s.settings)

	mux.Handle("/", s.auth.Require(protected))
}

type page struct {
	Title    string
	Nav      string
	Warn     bool
	Summary  monitor.Summary
	Now      time.Time
	Data     any
	Flash    string
	NextPath string
}

func (s *Server) render(w http.ResponseWriter, name string, p page) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "unknown page", http.StatusInternalServerError)
		return
	}
	p.Warn = s.auth.UsingDefaults
	p.Now = time.Now()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	entry := "layout"
	if name == "login" {
		entry = "login"
	}
	if err := t.ExecuteTemplate(w, entry, p); err != nil {
		s.log.Error("render failed", "page", name, "err", err)
	}
}

func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth.Authenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login", page{Title: "Sign in", NextPath: r.URL.Query().Get("next")})
}

func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user, pass := r.PostFormValue("username"), r.PostFormValue("password")
	if !s.auth.Verify(user, pass) {
		// A deliberate pause: enough to make repeated guessing impractical without
		// building out lockout state.
		time.Sleep(400 * time.Millisecond)
		s.log.Warn("failed login", "user", user, "remote", r.RemoteAddr)
		s.render(w, "login", page{
			Title: "Sign in", Flash: "Incorrect username or password.",
			NextPath: r.PostFormValue("next"),
		})
		return
	}
	s.auth.SetCookie(w, r.TLS != nil)
	next := r.PostFormValue("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type dashboardData struct {
	Monitors []monitor.Row
	MaxBeats int
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	summary, err := s.engine.Summary(r.Context())
	if err != nil {
		s.fail(w, "load summary", err)
		return
	}
	rows, err := s.engine.Rows(r.Context())
	if err != nil {
		s.fail(w, "load monitors", err)
		return
	}
	max := 1
	for _, row := range rows {
		for _, b := range row.Spark {
			if b.Beats > max {
				max = b.Beats
			}
		}
	}
	s.render(w, "dashboard", page{
		Title: "Monitors", Nav: "monitors", Summary: summary,
		Data: dashboardData{Monitors: rows, MaxBeats: max},
	})
}

type monitorData struct {
	Row       monitor.Row
	Config    map[string]any
	Activity  []monitor.Bucket
	MaxBeats  int
	Incidents []monitor.Incident
}

func (s *Server) monitorDetail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	row, err := s.engine.Row(r.Context(), slug)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "load monitor", err)
		return
	}

	summary, err := s.engine.Summary(r.Context())
	if err != nil {
		s.fail(w, "load summary", err)
		return
	}
	cfg, err := s.engine.Config(r.Context(), slug)
	if err != nil {
		s.fail(w, "load config", err)
		return
	}
	activity, err := s.engine.Activity(r.Context(), row.ID, 2*time.Hour)
	if err != nil {
		s.fail(w, "load activity", err)
		return
	}
	incidents, err := s.engine.Incidents(r.Context(), row.ID, 25)
	if err != nil {
		s.fail(w, "load incidents", err)
		return
	}

	max := 1
	for _, b := range activity {
		if b.Beats > max {
			max = b.Beats
		}
	}
	s.render(w, "monitor", page{
		Title: slug, Nav: "monitors", Summary: summary,
		Data: monitorData{Row: row, Config: cfg, Activity: activity, MaxBeats: max, Incidents: incidents},
	})
}

func (s *Server) mute(muted bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if err := s.engine.SetMuted(r.Context(), slug, muted); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			s.fail(w, "update mute", err)
			return
		}
		s.log.Info("monitor mute changed", "monitor", slug, "muted", muted)
		http.Redirect(w, r, "/monitors/"+slug, http.StatusSeeOther)
	}
}

func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	summary, err := s.engine.Summary(r.Context())
	if err != nil {
		s.fail(w, "load summary", err)
		return
	}
	list, err := s.engine.Incidents(r.Context(), 0, 100)
	if err != nil {
		s.fail(w, "load incidents", err)
		return
	}
	s.render(w, "incidents", page{
		Title: "Incidents", Nav: "incidents", Summary: summary, Data: list,
	})
}

type settingsData struct {
	Projects []monitor.Project
	Channels []monitor.Channel
	Host     string
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	summary, err := s.engine.Summary(r.Context())
	if err != nil {
		s.fail(w, "load summary", err)
		return
	}
	projects, err := s.engine.Projects(r.Context())
	if err != nil {
		s.fail(w, "load projects", err)
		return
	}
	channels, err := s.engine.Channels(r.Context())
	if err != nil {
		s.fail(w, "load channels", err)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	s.render(w, "settings", page{
		Title: "Settings", Nav: "settings", Summary: summary,
		Data: settingsData{Projects: projects, Channels: channels, Host: scheme + "://" + r.Host},
	})
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.log.Error("ui query failed", "what", what, "err", err)
	http.Error(w, "Something went wrong loading this page. The database may be unreachable.",
		http.StatusServiceUnavailable)
}

var funcs = template.FuncMap{
	"ago": func(t *time.Time) string {
		if t == nil {
			return "never"
		}
		return shortDuration(time.Since(*t)) + " ago"
	},
	"clock": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Local().Format("15:04:05")
	},
	"stamp": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.Local().Format("2 Jan 15:04:05")
	},
	"since": func(t time.Time) string { return shortDuration(time.Since(t)) },
	"secs":  func(n int64) string { return shortDuration(time.Duration(n) * time.Second) },
	"dur":   shortDuration,
	"pct": func(f float64) string {
		if f >= 99.995 {
			return "100%"
		}
		return fmt.Sprintf("%.2f%%", math.Floor(f*100)/100)
	},
	"progress": func(p *int64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprint(*p)
	},
	// barHeight maps a bucket's beat count onto the sparkline, never returning 0
	// for a bucket that had activity — an invisible bar reads as "no beats".
	"barHeight": func(beats, max int) int {
		if beats <= 0 {
			return 2
		}
		h := int(float64(beats) / float64(max) * 100)
		if h < 12 {
			h = 12
		}
		return h
	},
	"json": func(v any) string { return fmt.Sprintf("%v", v) },
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
