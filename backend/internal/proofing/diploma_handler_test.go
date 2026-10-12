package proofing

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// uploadPart is one part of a test upload: a file when name is set, else a
// plain form value.
type uploadPart struct {
	field, name string
	size        int
}

// diplomaUpload is a multipart upload request of parts.
func diplomaUpload(t *testing.T, parts ...uploadPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		var err error
		var w interface{ Write([]byte) (int, error) }
		if p.name != "" {
			w, err = mw.CreateFormFile(p.field, p.name)
		} else {
			w, err = mw.CreateFormField(p.field)
		}
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := w.Write(bytes.Repeat([]byte("x"), p.size)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close upload: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/diplomas", &body)
	r.Header.Set(headerContentType, mw.FormDataContentType())
	return r
}

// noTempFiles points the temporary directory at an empty one and fails the
// test when anything was written there by its end.
func noTempFiles(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Cleanup(func() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("read temp dir: %v", err)
		}
		for _, e := range entries {
			t.Errorf("the upload spilled to %s", e.Name())
		}
	})
}

// An upload of several extracts, larger together than the multipart form's
// memory, is read whole without a temporary file; other fields are skipped.
func TestReadDiplomaFilesInMemory(t *testing.T) {
	noTempFiles(t)
	r := diplomaUpload(t,
		uploadPart{field: "note", size: 10},
		uploadPart{field: diplomaFormField, name: "a.pdf", size: MaxDiplomaFileBytes},
		uploadPart{field: diplomaFormField, name: "b.pdf", size: MaxDiplomaFileBytes},
		uploadPart{field: "other", name: "c.pdf", size: 10},
	)
	files, err := readDiplomaFiles(httptest.NewRecorder(), r)
	if err != nil {
		t.Fatalf("readDiplomaFiles: %v", err)
	}
	if len(files) != 2 || files[0].Name != "a.pdf" || files[1].Name != "b.pdf" ||
		len(files[0].Bytes) != MaxDiplomaFileBytes || len(files[1].Bytes) != MaxDiplomaFileBytes {
		t.Errorf("files = %d, want a.pdf and b.pdf whole", len(files))
	}
}

func TestReadDiplomaFilesRefused(t *testing.T) {
	tooMany := make([]uploadPart, MaxDiplomasPerRequest+1)
	for i := range tooMany {
		tooMany[i] = uploadPart{field: diplomaFormField, name: "a.pdf", size: 1}
	}
	for _, tc := range []struct {
		name   string
		req    func(t *testing.T) *http.Request
		status int
		code   string
	}{
		{"a file too large", func(t *testing.T) *http.Request {
			return diplomaUpload(t, uploadPart{field: diplomaFormField, name: "a.pdf", size: MaxDiplomaFileBytes + 1})
		}, http.StatusRequestEntityTooLarge, "too_large"},
		{"the upload too large", func(t *testing.T) *http.Request {
			return diplomaUpload(t, uploadPart{field: diplomaFormField, name: "a.pdf", size: 1},
				uploadPart{field: "padding", size: maxDiplomaUploadBytes})
		}, http.StatusRequestEntityTooLarge, "too_large"},
		{"too many files", func(t *testing.T) *http.Request {
			return diplomaUpload(t, tooMany...)
		}, http.StatusBadRequest, "too_many_files"},
		{"no file", func(t *testing.T) *http.Request {
			return diplomaUpload(t, uploadPart{field: diplomaFormField, size: 1})
		}, http.StatusBadRequest, "invalid_body"},
		{"not multipart", func(t *testing.T) *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/diplomas", strings.NewReader("{}"))
			r.Header.Set(headerContentType, contentTypeJSON)
			return r
		}, http.StatusBadRequest, "invalid_body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noTempFiles(t)
			_, err := readDiplomaFiles(httptest.NewRecorder(), tc.req(t))
			var apiErr *respond.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status || apiErr.Code != tc.code {
				t.Errorf("readDiplomaFiles = %v, want %d %s", err, tc.status, tc.code)
			}
		})
	}
}
