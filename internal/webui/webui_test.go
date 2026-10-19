package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestHandler(t *testing.T) {
	h := handler(fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app-123.js":    {Data: []byte("console.log(1)")},
		"sw.js":                {Data: []byte("self")},
		"manifest.webmanifest": {Data: []byte("{}")},
	})

	tests := []struct {
		path, body, cache string
	}{
		{"/", "<html>app</html>", "no-cache"},
		{"/books/42", "<html>app</html>", "no-cache"}, // client-side route
		{"/assets/app-123.js", "console.log(1)", "public, max-age=31536000, immutable"},
		{"/assets/missing.js", "<html>app</html>", "no-cache"},
		// Installed apps pick up a new version.
		{"/sw.js", "self", "no-cache"},
		{"/manifest.webmanifest", "{}", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.body, rec.Body.String())
			assert.Equal(t, tt.cache, rec.Header().Get("Cache-Control"))
		})
	}
}

func TestManifestType(t *testing.T) {
	h := handler(fstest.MapFS{"index.html": {}, "manifest.webmanifest": {Data: []byte("{}")}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	assert.Equal(t, "application/manifest+json", rec.Header().Get("Content-Type"))
}

func TestHandlerWithoutBuild(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "frontend not built")
}
