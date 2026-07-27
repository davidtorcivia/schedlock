package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Templates and static assets are embedded in the binary rather than read from
// disk at runtime. The server then has no dependency on its working directory,
// and a deployment cannot end up serving a page that does not match the code.
//
//go:embed assets/templates/*.html
var templateFS embed.FS

//go:embed assets/static
var staticFS embed.FS

// StaticHandler serves the embedded static assets under /static/.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "assets/static")
	if err != nil {
		panic("web: embedded static assets are unavailable: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assets are versioned with the binary, so they may be cached, but not
		// so aggressively that an upgrade leaves stale scripts behind.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("X-Content-Type-Options", "nosniff")
		}
		fileServer.ServeHTTP(w, r)
	}))
}
