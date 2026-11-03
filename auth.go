package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Roles. Staff sign in with a PIN for the role they need. The admin role also
// opens the kitchen screens, so the owner needs only one login.
const (
	RoleAdmin   = "admin"
	RoleKitchen = "kitchen"

	sessionDuration  = 8 * time.Hour
	maxLoginAttempts = 5
	lockoutDuration  = 15 * time.Minute
)

// auth holds the PINs, the signing secret and the login rate limiter.
type auth struct {
	pins   map[string][]byte // role -> PIN
	secret []byte

	mu       sync.Mutex
	failures map[string]*failure // client IP -> recent failures
}

type failure struct {
	count int
	until time.Time // lockout end, zero when not locked
}

func newAuth(adminPIN, kitchenPIN string, secret []byte) *auth {
	return &auth{
		pins:     map[string][]byte{RoleAdmin: []byte(adminPIN), RoleKitchen: []byte(kitchenPIN)},
		secret:   secret,
		failures: map[string]*failure{},
	}
}

func (a *auth) sign(msg string) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// token is "role|expiry|signature". The signature covers role and expiry, so
// neither can be edited in the browser.
func (a *auth) token(role string, expires time.Time) string {
	payload := role + "|" + strconv.FormatInt(expires.Unix(), 10)
	return payload + "|" + a.sign(payload)
}

// parseToken returns the role if the token is genuine and unexpired.
func (a *auth) parseToken(tok string) (string, bool) {
	parts := strings.Split(tok, "|")
	if len(parts) != 3 {
		return "", false
	}
	payload := parts[0] + "|" + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(a.sign(payload))) {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}

func cookieName(role string) string { return "takeaway_" + role }

// roleOf reports whether the request carries a valid session that allows the
// wanted role. Admin satisfies every role and is checked first, so a browser holding both
// cookies is always treated as admin.
func (a *auth) roleOf(r *http.Request, want string) (string, bool) {
	for _, role := range []string{RoleAdmin, want} {
		c, err := r.Cookie(cookieName(role))
		if err != nil {
			continue
		}
		if got, ok := a.parseToken(c.Value); ok && got == role {
			return role, true
		}
	}
	return "", false
}

func (a *auth) setCookie(w http.ResponseWriter, r *http.Request, role string) {
	exp := time.Now().Add(sessionDuration)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(role),
		Value:    a.token(role, exp),
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
	})
}

func (a *auth) clearCookie(w http.ResponseWriter, role string) {
	http.SetCookie(w, &http.Cookie{Name: cookieName(role), Path: "/", MaxAge: -1, HttpOnly: true})
}

// csrfToken is derived from the role, not the cookie, so it stays valid when
// the session cookie is reissued.
func (a *auth) csrfToken(role string) string { return a.sign("csrf|" + role) }

// validCSRF checks the form field or X-CSRF-Token header against the token for
// the role that is actually signed in.
func (a *auth) validCSRF(r *http.Request, role string) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		got = r.FormValue("csrf_token")
	}
	return got != "" && hmac.Equal([]byte(got), []byte(a.csrfToken(role)))
}

func (a *auth) checkPIN(role, pin string) bool {
	want, ok := a.pins[role]
	if !ok || len(want) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(want, []byte(pin)) == 1
}

// locked reports whether the client is in a lockout, and for how long.
func (a *auth) locked(ip string) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.failures[ip]
	if f == nil || f.until.IsZero() {
		return 0, false
	}
	if left := time.Until(f.until); left > 0 {
		return left, true
	}
	delete(a.failures, ip) // lockout over
	return 0, false
}

func (a *auth) recordFailure(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.failures[ip]
	if f == nil {
		f = &failure{}
		a.failures[ip] = f
	}
	f.count++
	if f.count >= maxLoginAttempts {
		f.until = time.Now().Add(lockoutDuration)
	}
}

func (a *auth) clearFailures(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, ip)
}

// require wraps a handler so it only runs for a signed-in user with the role.
// Browsers asking for HTML get sent to the login page; fetch() calls get 401.
func (s *server) require(role string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if _, ok := s.auth.roleOf(r, role); !ok {
			if strings.Contains(r.Header.Get("Accept"), "application/json") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/"+role+"/login", http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// clientIP uses the first X-Forwarded-For hop (set by Google's front end),
// falling back to the socket address when running locally.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
