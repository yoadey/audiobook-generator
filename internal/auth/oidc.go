package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config configures OpenID Connect.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string // empty for a public client (PKCE only)
	Scopes       []string
	// PublicURL is the external base URL; empty derives it from requests.
	PublicURL string
	// SessionKey encrypts the cookies (at least 32 bytes).
	SessionKey []byte
	// SessionTTL is how long a sign-in lasts; it is extended while used.
	SessionTTL time.Duration
	Log        *slog.Logger
}

// Authenticator signs users in. A nil *Authenticator means authentication
// is off.
type Authenticator struct {
	cfg    Config
	sealer *sealer

	mu         sync.Mutex
	provider   *oidc.Provider
	endSession string
}

const (
	sessionCookie = "abg_session"
	loginCookie   = "abg_login"
	loginTTL      = 10 * time.Minute
)

// New validates cfg. The issuer is contacted on the first sign-in, so the
// service starts while the identity provider is unreachable.
func New(cfg Config) (*Authenticator, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, errors.New("OIDC needs an issuer and a client ID")
	}
	s, err := newSealer(cfg.SessionKey)
	if err != nil {
		return nil, err
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 30 * 24 * time.Hour
	}
	return &Authenticator{cfg: cfg, sealer: s}, nil
}

// session is the content of the session cookie.
type session struct {
	User
	Issued int64 `json:"iat"`
}

type login struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Return   string `json:"r"`
}

func (a *Authenticator) discover(ctx context.Context) (*oidc.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, a.cfg.Issuer)
	if err != nil {
		return nil, err
	}
	var extra struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&extra)
	a.provider, a.endSession = p, extra.EndSession
	return p, nil
}

func (a *Authenticator) oauth(p *oidc.Provider, r *http.Request) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.cfg.ClientID,
		ClientSecret: a.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  BaseURL(r, a.cfg.PublicURL) + "/auth/callback",
		Scopes:       a.cfg.Scopes,
	}
}

// Register mounts /auth/login, /auth/callback and /auth/logout.
func (a *Authenticator) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.login)
	mux.HandleFunc("GET /auth/callback", a.callback)
	mux.HandleFunc("GET /auth/logout", a.logout)
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeReturn accepts only local paths (no open redirects).
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) ||
		strings.HasPrefix(p, "/auth/") {
		return "/"
	}
	return p
}

func (a *Authenticator) login(w http.ResponseWriter, r *http.Request) {
	p, err := a.discover(r.Context())
	if err != nil {
		a.cfg.Log.ErrorContext(r.Context(), "OIDC discovery failed", "issuer", a.cfg.Issuer, "err", err)
		http.Error(w, "the sign-in service is unavailable, try again later", http.StatusServiceUnavailable)
		return
	}
	l := login{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Return: safeReturn(r.URL.Query().Get("redirect"))}
	value, err := a.sealer.seal(loginCookie, l, loginTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.setCookie(w, r, loginCookie, value, "/auth/", loginTTL)
	target := a.oauth(p, r).AuthCodeURL(l.State, oidc.Nonce(l.Nonce), oauth2.S256ChallengeOption(l.Verifier))
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // the provider's endpoint from discovery
}

func (a *Authenticator) callback(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string, err error) {
		a.cfg.Log.WarnContext(r.Context(), "sign-in failed", "reason", msg, "err", err)
		http.Error(w, "sign-in failed: "+msg+" (try again from the start page)", http.StatusBadRequest)
	}
	c, err := r.Cookie(loginCookie)
	if err != nil {
		fail("no sign-in in progress", err)
		return
	}
	var l login
	if err := a.sealer.open(loginCookie, c.Value, &l); err != nil {
		fail("sign-in expired", err)
		return
	}
	a.setCookie(w, r, loginCookie, "", "/auth/", -1)
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		fail(e+": "+q.Get("error_description"), nil)
		return
	}
	if q.Get("state") == "" || q.Get("state") != l.State {
		fail("state mismatch", nil)
		return
	}
	p, err := a.discover(r.Context())
	if err != nil {
		fail("identity provider unavailable", err)
		return
	}
	tok, err := a.oauth(p, r).Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(l.Verifier))
	if err != nil {
		fail("code exchange", err)
		return
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		fail("no ID token", nil)
		return
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: a.cfg.ClientID}).Verify(r.Context(), raw)
	if err != nil {
		fail("invalid ID token", err)
		return
	}
	if idt.Nonce != l.Nonce {
		fail("nonce mismatch", nil)
		return
	}
	var claims struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
		Email    string `json:"email"`
	}
	if err := idt.Claims(&claims); err != nil {
		fail("claims", err)
		return
	}
	u := User{Subject: idt.Subject, Email: claims.Email}
	for _, n := range []string{claims.Name, claims.Username, claims.Email, idt.Subject} {
		if n != "" {
			u.Name = n
			break
		}
	}
	if err := a.startSession(w, r, u); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.cfg.Log.InfoContext(r.Context(), "signed in", "subject", u.Subject)
	http.Redirect(w, r, l.Return, http.StatusFound)
}

func (a *Authenticator) startSession(w http.ResponseWriter, r *http.Request, u User) error {
	value, err := a.sealer.seal(sessionCookie, session{User: u, Issued: time.Now().Unix()}, a.cfg.SessionTTL)
	if err != nil {
		return err
	}
	a.setCookie(w, r, sessionCookie, value, "/", a.cfg.SessionTTL)
	return nil
}

func (a *Authenticator) logout(w http.ResponseWriter, r *http.Request) {
	a.setCookie(w, r, sessionCookie, "", "/", -1)
	target := "/"
	a.mu.Lock()
	end := a.endSession
	a.mu.Unlock()
	if end != "" {
		if u, err := url.Parse(end); err == nil {
			q := u.Query()
			q.Set("client_id", a.cfg.ClientID)
			q.Set("post_logout_redirect_uri", BaseURL(r, a.cfg.PublicURL)+"/")
			u.RawQuery = q.Encode()
			target = u.String()
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (a *Authenticator) setCookie(w http.ResponseWriter, r *http.Request, name, value, path string, ttl time.Duration) {
	// Secure whenever the service is reached over https (plain http only
	// in local setups, where a Secure cookie would never be sent back).
	c := &http.Cookie{ //nolint:gosec // see above
		Name: name, Value: value, Path: path, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(BaseURL(r, a.cfg.PublicURL), "https://"),
	}
	if ttl < 0 {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(ttl.Seconds())
	}
	http.SetCookie(w, c)
}

// public lists paths that work without a session: probes, metrics (scraped
// in-cluster), podcast feeds (token-protected) and the sign-in itself.
func public(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics":
		return true
	}
	return strings.HasPrefix(path, "/feed/") || strings.HasPrefix(path, "/auth/")
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// Middleware requires a session everywhere but on public paths and puts
// the user into the request context. Without authentication (nil
// receiver) it passes everything through.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if public(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		var s session
		c, err := r.Cookie(sessionCookie)
		if err == nil {
			err = a.sealer.open(sessionCookie, c.Value, &s)
		}
		if err != nil {
			a.unauthorized(w, r)
			return
		}
		// Cookies are SameSite=Lax; refuse cross-site writes outright too.
		if unsafeMethod(r.Method) {
			if o := r.Header.Get("Origin"); o != "" && o != origin(BaseURL(r, a.cfg.PublicURL)) {
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		// A session in use is extended.
		if time.Since(time.Unix(s.Issued, 0)) > a.cfg.SessionTTL/2 {
			if err := a.startSession(w, r, s.User); err != nil {
				a.cfg.Log.WarnContext(r.Context(), "extend session", "err", err)
			}
		}
		u := s.User
		inner := r.WithContext(WithUser(r.Context(), &u))
		next.ServeHTTP(w, inner)
		r.Pattern = inner.Pattern // for the metrics' route label

	})
}

func (a *Authenticator) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || unsafeMethod(r.Method) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"message":"sign-in required"}`)
		return
	}
	http.Redirect(w, r, "/auth/login?redirect="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
}

// origin returns scheme://host of a base URL.
func origin(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	return u.Scheme + "://" + u.Host
}

// BaseURL is the configured public URL, else the one the request came in
// on (https when TLS or a proxy's X-Forwarded-Proto says so).
func BaseURL(r *http.Request, public string) string {
	if public != "" {
		return strings.TrimRight(public, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
