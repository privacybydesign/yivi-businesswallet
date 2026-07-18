package server

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

// spaHandler serves a built single-page application from staticPath. If the
// requested path maps to an existing file it is served directly; otherwise the
// index document is served so client-side routing can resolve the path. This is
// the standard SPA-serving pattern (see the gorilla/mux README example),
// adapted to stdlib net/http.
//
// It is mounted on the root mux only, so it never shadows the API subtree or the
// health endpoints: ServeMux always prefers the more specific pattern
// (/api/v1/, /livez, /readyz) over "/".
type spaHandler struct {
	staticPath string
	indexPath  string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The joined path is only used to decide whether a matching file exists; it
	// is never served from directly. Note filepath.Join does NOT keep the result
	// inside staticPath (Join("/frontend", "../secret") == "/secret"), so it is
	// not the traversal guard. The guard is twofold: ServeMux cleans/redirects
	// request paths containing ".." before this handler runs, and every actual
	// read below re-derives from r.URL.Path through http.ServeFile /
	// http.FileServer(http.Dir(...)), both of which reject "..". A stat that
	// happens to resolve outside staticPath only routes us to the index fallback
	// or the FileServer, neither of which serves that outside path.
	path := filepath.Join(h.staticPath, r.URL.Path)

	fi, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && fi.IsDir()) {
		// Unknown path or a directory: hand it to the SPA, which resolves the
		// route client-side.
		http.ServeFile(w, r, filepath.Join(h.staticPath, h.indexPath))
		return
	}
	if err != nil {
		// Stat failed for a reason other than "not found" (e.g. a permission
		// error). Log it server-side and return a generic 500 rather than leak
		// filesystem details. Checked before dereferencing fi, which is nil on
		// any non-nil error.
		slog.ErrorContext(r.Context(), "spa static stat failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}
