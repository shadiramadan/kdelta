// Package ui serves the embedded Next.js static export. The dist directory is
// populated by `task ui:embed`; source builds without it still compile and
// serve a hint instead of the UI.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded web UI, or an explanatory placeholder when the
// binary was built without one (e.g. plain `go build` before `task build`).
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: embedded dist directory missing: " + err.Error())
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "kdelta was built without the web UI - run `task build` to embed it (or `task dev` for the UI dev server)", http.StatusNotImplemented)
		})
	}
	return http.FileServerFS(sub)
}
