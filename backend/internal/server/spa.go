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
	// filepath.Join calls path.Clean internally, which resolves any ".."
	// segments before joining and so prevents directory traversal outside
	// staticPath.
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
