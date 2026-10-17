package kube

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaler(t *testing.T) {
	var patched string
	replicas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/apis/apps/v1/namespaces/books/deployments/vd/scale":
			assert.Equal(t, "application/merge-patch+json", r.Header.Get("Content-Type"))
			b, _ := io.ReadAll(r.Body)
			patched = string(b)
			replicas = 1
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/books/deployments/vd":
			_, _ = w.Write([]byte(`{"spec":{"replicas":` + map[int]string{0: "0", 1: "1"}[replicas] +
				`},"status":{"readyReplicas":` + map[int]string{0: "0", 1: "1"}[replicas] + `}}`))
		default:
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		}
	}))
	defer srv.Close()
	token := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(token, []byte("tok\n"), 0o600))
	s := New(srv.URL, "books", "vd", token, srv.Client())
	ctx := context.Background()

	st, err := s.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Replicas: 0, Ready: 0}, st)
	require.NoError(t, s.Scale(ctx, 1))
	assert.JSONEq(t, `{"spec":{"replicas":1}}`, patched)
	st, err = s.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, Status{Replicas: 1, Ready: 1}, st)

	other := New(srv.URL, "books", "other", token, srv.Client())
	_, err = other.Status(ctx)
	assert.ErrorContains(t, err, "403")

	_, err = InCluster("vd")
	assert.Error(t, err, "not in a cluster")
}
