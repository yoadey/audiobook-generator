// Package webui serves the React single-page app embedded at build time.
//
// The Vite build writes into ./dist (see web/vite.config.ts). The directory
// is committed with only a .gitkeep, so Go builds and tests work without a
// frontend build; the handler then explains how to build it.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves static assets and falls back to index.html for client-side
// routes.
func Handler() http.Handler {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist exists
	}
	return handler(root)
}

func handler(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(root, name); err == nil && !st.IsDir() {
				switch {
				case strings.HasPrefix(name, "assets/"):
					// Vite fingerprints everything under assets/.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				case name == "sw.js" || name == "manifest.webmanifest":
					// A new version of the app reaches installed copies.
					w.Header().Set("Cache-Control", "no-cache")
				}
				if path.Ext(name) == ".webmanifest" {
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				files.ServeHTTP(w, r)
				return
			}
		}

		index, err := fs.ReadFile(root, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "frontend not built: run `make web` (or `npm run build` in web/)", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
