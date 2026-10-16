package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/auth"
)

// fakeIdP is a minimal OpenID provider: discovery, keys, token endpoint.
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	signer jose.Signer

	mu        sync.Mutex
	challenge string // code_challenge of the last authorization
	nonce     string // nonce to put into the next ID token
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithHeader("kid", "k1"))
	require.NoError(t, err)
	f := &fakeIdP{t: t, key: key, signer: signer}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/keys",
			"end_session_endpoint":                  f.srv.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		f.mu.Lock()
		challenge, nonce := f.challenge, f.nonce
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		now := time.Now()
		raw, err := jwt.Signed(f.signer).Claims(jwt.Claims{
			Issuer: f.srv.URL, Subject: "user-42", Audience: jwt.Audience{"abg"},
			IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Hour)),
		}).Claims(map[string]any{"nonce": nonce, "name": "Erin Solstice", "email": "erin@example.org"}).Serialize()
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": raw,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func newApp(t *testing.T, idp *fakeIdP) (*httptest.Server, *http.Client) {
	t.Helper()
	a, err := auth.New(auth.Config{
		Issuer: idp.srv.URL, ClientID: "abg", SessionKey: []byte(strings.Repeat("k", 32)),
		SessionTTL: time.Hour, Log: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	a.Register(mux)
	mux.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, auth.Subject(r.Context()))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("/feed/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "feed") })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") })
	app := httptest.NewServer(auth.SameOrigin("", a.Middleware(mux)))
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return app, client
}

// result is what the tests look at of a response (the body is read and
// closed in do).
type result struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, c *http.Client, method, u string, header map[string]string) (result, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, nil)
	require.NoError(t, err)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return result{StatusCode: res.StatusCode, Header: res.Header}, string(body)
}

// signIn runs the authorization code flow up to the callback and returns
// the callback's response.
func signIn(t *testing.T, idp *fakeIdP, app *httptest.Server, c *http.Client, redirect string, tamper func(q url.Values)) result {
	t.Helper()
	res, _ := do(t, c, http.MethodGet, app.URL+"/auth/login?redirect="+url.QueryEscape(redirect), nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(loc.String(), idp.srv.URL+"/authorize"), loc.String())
	q := loc.Query()
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, app.URL+"/auth/callback", q.Get("redirect_uri"))
	assert.Contains(t, q.Get("scope"), "openid")
	idp.mu.Lock()
	idp.challenge, idp.nonce = q.Get("code_challenge"), q.Get("nonce")
	idp.mu.Unlock()

	cb := url.Values{"code": {"good-code"}, "state": {q.Get("state")}}
	if tamper != nil {
		tamper(cb)
	}
	res, _ = do(t, c, http.MethodGet, app.URL+"/auth/callback?"+cb.Encode(), nil)
	return res
}

func TestSignIn(t *testing.T) {
	idp := newIdP(t)
	app, c := newApp(t, idp)

	// Without a session: the API answers 401, pages redirect to sign-in;
	// probes and feeds stay open.
	res, body := do(t, c, http.MethodGet, app.URL+"/api/whoami", nil)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	assert.JSONEq(t, `{"message":"sign-in required"}`, body)
	res, _ = do(t, c, http.MethodGet, app.URL+"/books/1?x=2", nil)
	assert.Equal(t, http.StatusFound, res.StatusCode)
	assert.Equal(t, "/auth/login?redirect=%2Fbooks%2F1%3Fx%3D2", res.Header.Get("Location"))
	res, body = do(t, c, http.MethodGet, app.URL+"/healthz", nil)
	assert.Equal(t, "ok", body)
	res, body = do(t, c, http.MethodGet, app.URL+"/feed/tok/1.xml", nil)
	assert.Equal(t, "feed", body)

	res = signIn(t, idp, app, c, "/books/1", nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	assert.Equal(t, "/books/1", res.Header.Get("Location"))
	res, body = do(t, c, http.MethodGet, app.URL+"/api/whoami", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "user-42", body)

	// Cross-site writes are refused, same-site ones pass.
	res, _ = do(t, c, http.MethodPost, app.URL+"/api/whoami", map[string]string{"Origin": "https://evil.example"})
	assert.Equal(t, http.StatusForbidden, res.StatusCode)
	res, _ = do(t, c, http.MethodPost, app.URL+"/api/whoami", map[string]string{"Origin": app.URL})
	assert.Equal(t, http.StatusOK, res.StatusCode)

	// A forged cookie is no session.
	u, _ := url.Parse(app.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, []*http.Cookie{{Name: "abg_session", Value: "Zm9yZ2Vk"}})
	forged := &http.Client{Jar: jar}
	res, _ = do(t, forged, http.MethodGet, app.URL+"/api/whoami", nil)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)

	// Sign-out clears the session and ends the provider's.
	res, _ = do(t, c, http.MethodGet, app.URL+"/auth/logout", nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, _ := url.Parse(res.Header.Get("Location"))
	assert.Equal(t, idp.srv.URL+"/logout", loc.Scheme+"://"+loc.Host+loc.Path)
	assert.Equal(t, app.URL+"/", loc.Query().Get("post_logout_redirect_uri"))
	res, _ = do(t, c, http.MethodGet, app.URL+"/api/whoami", nil)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestSignInFailures(t *testing.T) {
	idp := newIdP(t)
	app, c := newApp(t, idp)

	// A forged state, a wrong code, a replayed nonce: no session.
	res := signIn(t, idp, app, c, "/", func(q url.Values) { q.Set("state", "forged") })
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	res = signIn(t, idp, app, c, "/", func(q url.Values) { q.Set("code", "bad-code") })
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	res = signIn(t, idp, app, c, "/", func(url.Values) {
		idp.mu.Lock()
		idp.nonce = "other"
		idp.mu.Unlock()
	})
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	res, _ = do(t, c, http.MethodGet, app.URL+"/api/whoami", nil)
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)

	// The login cookie is single-use: a second callback has none.
	res = signIn(t, idp, app, c, "/", nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	res, _ = do(t, c, http.MethodGet, app.URL+"/auth/callback?code=good-code&state=x", nil)
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)

	// No open redirects after sign-in.
	for _, r := range []string{"//evil.example/x", "https://evil.example", `/\evil.example`, "/auth/logout"} {
		c2 := &http.Client{CheckRedirect: c.CheckRedirect}
		c2.Jar, _ = cookiejar.New(nil)
		res = signIn(t, idp, app, c2, r, nil)
		require.Equal(t, http.StatusFound, res.StatusCode)
		assert.Equal(t, "/", res.Header.Get("Location"), r)
	}
}

func TestSignInProviderDown(t *testing.T) {
	a, err := auth.New(auth.Config{
		Issuer: "http://127.0.0.1:1", ClientID: "abg", SessionKey: []byte(strings.Repeat("k", 32)),
		Log: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	a.Register(mux)
	w := httptest.NewRecorder()
	a.Middleware(mux).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	_, err = auth.New(auth.Config{Issuer: "http://x", ClientID: "abg", SessionKey: []byte("short")})
	assert.Error(t, err)
}

func TestNilAuthenticatorPassesThrough(t *testing.T) {
	var a *auth.Authenticator
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "subject="+auth.Subject(r.Context()))
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/books", nil))
	assert.Equal(t, "subject=", w.Body.String())
}

func TestSameOriginWithoutSignIn(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	h := auth.SameOrigin("", ok)
	post := func(origin, host string) int {
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/books", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	assert.Equal(t, http.StatusForbidden, post("https://evil.example", "abg.lan"))
	assert.Equal(t, http.StatusOK, post("https://abg.lan", "abg.lan"), "scheme behind a proxy is not compared")
	assert.Equal(t, http.StatusOK, post("", "abg.lan"))
	assert.Equal(t, http.StatusForbidden, post("null", "abg.lan"))

	public := auth.SameOrigin("https://audio.example.org", ok)
	r := httptest.NewRequest(http.MethodPost, "http://internal:8080/api/books", nil)
	r.Header.Set("Origin", "https://audio.example.org")
	w := httptest.NewRecorder()
	public.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSafeReturn(t *testing.T) {
	idp := newIdP(t)
	app, c := newApp(t, idp)
	for in, want := range map[string]string{
		"/books/1?x=2":      "/books/1?x=2",
		"/\t/evil.example":  "/",
		"/%09/evil.example": "/%09/evil.example",
		"/a\\b":             "/",
		"http:/evil":        "/",
	} {
		res := signIn(t, idp, app, c, in, nil)
		assert.Equal(t, want, res.Header.Get("Location"), in)
	}
}
