package server

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	// Vite names every file under /assets/ after a hash of its content, so a
	// given URL never changes and a browser may keep it for good.
	hashedAssetsPrefix = "/assets/"
	immutableCache     = "public, max-age=31536000, immutable"
	// The index document names the current hashed files, so it is revalidated
	// on every visit or a deploy would not reach an open browser.
	revalidateCache = "no-cache"
)

// compressibleTypes are the text formats gzip shrinks; images and woff2 fonts
// are compressed already.
var compressibleTypes = []string{"text/", "application/javascript", "application/json", "image/svg+xml"}

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
	// pageHeaderers set headers on the index document per client-side route.
	pageHeaderers []PageHeaderer
	// gzipped holds each hashed asset compressed once, by request path.
	gzipped *sync.Map
}

func newSPAHandler(staticPath string, pageHeaderers []PageHeaderer) spaHandler {
	return spaHandler{staticPath: staticPath, indexPath: spaIndex, pageHeaderers: pageHeaderers, gzipped: &sync.Map{}}
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
	if os.IsNotExist(err) && strings.HasPrefix(r.URL.Path, hashedAssetsPrefix) {
		// A hashed file that is gone (a tab from before a deploy asks for its old
		// chunk) is never a client route. The index document here would be read
		// as a broken script; a 404 lets the page reload itself.
		http.NotFound(w, r)
		return
	}
	if os.IsNotExist(err) || (err == nil && fi.IsDir()) {
		// Unknown path or a directory: hand it to the SPA, which resolves the
		// route client-side.
		for _, ph := range h.pageHeaderers {
			ph.PageHeaders(r, w.Header())
		}
		w.Header().Set("Cache-Control", revalidateCache)
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

	if strings.HasPrefix(r.URL.Path, hashedAssetsPrefix) {
		w.Header().Set("Cache-Control", immutableCache)
		if h.serveGzipped(w, r, path) {
			return
		}
	} else {
		w.Header().Set("Cache-Control", revalidateCache)
	}

	http.FileServer(http.Dir(h.staticPath)).ServeHTTP(w, r)
}

// serveGzipped answers a hashed asset compressed and reports whether it did.
// It leaves range requests, clients without gzip and formats that do not
// shrink to the file server. path was stat-ed by the caller and sits under
// /assets/ of a ServeMux-cleaned URL, so it is inside staticPath.
func (h spaHandler) serveGzipped(w http.ResponseWriter, r *http.Request, path string) bool {
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" || !compressible(contentType) {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}

	body, err := h.gzippedAsset(r.URL.Path, path)
	if err != nil {
		slog.ErrorContext(r.Context(), "spa asset compression failed", slog.String("error", err.Error()))
		return false
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if _, err := w.Write(body); err != nil {
		slog.ErrorContext(r.Context(), "spa asset write failed", slog.String("error", err.Error()))
	}
	return true
}

func (h spaHandler) gzippedAsset(urlPath, path string) ([]byte, error) {
	if cached, ok := h.gzipped.Load(urlPath); ok {
		return cached.([]byte), nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is a stat-ed file under staticPath, see serveGzipped
	if err != nil {
		return nil, fmt.Errorf("reading asset: %w", err)
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("gzip writer: %w", err)
	}
	if _, err := zw.Write(raw); err != nil {
		return nil, fmt.Errorf("compressing asset: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compressing asset: %w", err)
	}
	h.gzipped.Store(urlPath, buf.Bytes())
	return buf.Bytes(), nil
}

func compressible(contentType string) bool {
	for _, prefix := range compressibleTypes {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}
