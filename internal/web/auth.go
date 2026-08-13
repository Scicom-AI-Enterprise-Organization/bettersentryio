package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "bsio_session"
	// DefaultPassword is the development default. It exists so a fresh install is
	// usable immediately; every code path that can see it also shouts about it.
	DefaultUser     = "admin"
	DefaultPassword = "12345"
)

type Auth struct {
	user string
	pass string
	key  []byte
	ttl  time.Duration

	// UsingDefaults is surfaced in the startup log and as a banner on every page.
	UsingDefaults bool
}

func NewAuth(user, pass string, ttl time.Duration) (*Auth, error) {
	if user == "" {
		user = DefaultUser
	}
	if pass == "" {
		pass = DefaultPassword
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	// A per-boot key means sessions do not survive a restart. That is the right
	// trade for one operator; a shared --session-key is what multiple replicas
	// would need, and is a later concern.
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session key: %w", err)
	}
	return &Auth{
		user:          user,
		pass:          pass,
		key:           key,
		ttl:           ttl,
		UsingDefaults: user == DefaultUser && pass == DefaultPassword,
	}, nil
}

// Verify compares in constant time so a wrong password cannot be found by timing.
func (a *Auth) Verify(user, pass string) bool {
	u := subtle.ConstantTimeCompare([]byte(user), []byte(a.user))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(a.pass))
	return u == 1 && p == 1
}

func (a *Auth) sign(payload string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) SetCookie(w http.ResponseWriter, secure bool) {
	payload := fmt.Sprintf("%s|%d", a.user, time.Now().Add(a.ttl).Unix())
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + a.sign(payload),
		Path:  "/",
		// Lax keeps the cookie off cross-site POSTs, which is the CSRF exposure
		// that matters for the mute/unmute actions.
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
		Secure:   secure,
		Expires:  time.Now().Add(a.ttl),
	})
}

func (a *Auth) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	encoded, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	payload := string(raw)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign(payload))) != 1 {
		return false
	}
	user, expStr, ok := strings.Cut(payload, "|")
	if !ok || user != a.user {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}

// Require sends unauthenticated browsers to the login page and unauthenticated
// API callers a 401, rather than redirecting a JSON client into HTML.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}`))
			return
		}
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
	})
}
