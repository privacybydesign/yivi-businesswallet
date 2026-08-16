package export

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const manifestPath = "manifest.json"

type recorder interface {
	RecordExport(ctx context.Context, orgID, bundleID uuid.UUID, sections []string) error
}

// Service assembles the bundle from the registered section writers.
type Service struct {
	writers []SectionWriter
	audit   recorder
	budget  int64
	now     func() time.Time
}

func NewService(audit recorder, writers []SectionWriter) *Service {
	return &Service{
		writers: writers,
		audit:   audit,
		budget:  unlimitedBudget,
		now:     time.Now,
	}
}

// Archive is a finished bundle. Close removes the temp files behind it.
type Archive struct {
	file     *os.File
	dir      string
	Size     int64
	BundleID uuid.UUID
	Filename string
}

func (a *Archive) Reader() io.Reader { return a.file }

func (a *Archive) Close() error {
	err := a.file.Close()
	if rmErr := os.RemoveAll(a.dir); err == nil {
		err = rmErr
	}
	return err
}

// Export builds the bundle for one organization. An empty sections slice means
// all of them. The audit event is written before the archive is returned, so
// nothing carrying members' personal data is served unrecorded.
func (s *Service) Export(ctx context.Context, org Organization, sections []string) (*Archive, error) {
	requested, err := s.resolveSections(sections)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "ybw-export-")
	if err != nil {
		return nil, fmt.Errorf("export: creating staging directory: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()

	stageDir := filepath.Join(dir, "stage")
	manifest, staged, err := s.stage(ctx, stageDir, org, requested)
	if err != nil {
		return nil, err
	}

	archivePath := filepath.Join(dir, "bundle.zip")
	if err := writeZip(archivePath, stageDir, manifest, staged); err != nil {
		return nil, err
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("export: opening bundle: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("export: sizing bundle: %w", err)
	}

	if err := s.audit.RecordExport(ctx, org.ID, manifest.BundleID, requested); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("export: recording export: %w", err)
	}

	success = true
	return &Archive{
		file:     file,
		dir:      dir,
		Size:     info.Size(),
		BundleID: manifest.BundleID,
		Filename: filename(org.Slug, manifest.GeneratedAt),
	}, nil
}

func (s *Service) stage(ctx context.Context, dir string, org Organization, requested []string) (Manifest, []FileEntry, error) {
	bundle := newBundle(dir, s.budget)

	byKey := make(map[string]SectionWriter, len(s.writers))
	for _, w := range s.writers {
		byKey[w.Key()] = w
	}

	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		BundleID:      uuid.New(),
		GeneratedAt:   timestamp(s.now()),
		Producer:      Producer{Name: producerName, Version: producerVersion()},
		Organization:  org,
		Sections:      []Section{},
	}

	var staged []FileEntry
	for _, key := range requested {
		writer, ok := byKey[key]
		if !ok {
			return Manifest{}, nil, fmt.Errorf("export: no writer registered for section %q", key)
		}
		section := bundle.section(key, sectionDirs[key])
		// A section that cannot be read fails the whole export: a bundle
		// silently missing a data point is indistinguishable from an
		// organization that holds none of it.
		if err := writer.Write(ctx, org.ID, section); err != nil {
			return Manifest{}, nil, fmt.Errorf("export: writing section %q: %w", key, err)
		}
		entry := section.manifest()
		manifest.Sections = append(manifest.Sections, entry)
		staged = append(staged, entry.Files...)
	}
	return manifest, staged, nil
}

func writeZip(archivePath, stageDir string, manifest Manifest, staged []FileEntry) error {
	out, err := os.Create(archivePath)
	if err != nil {
		return fmt.Errorf("export: creating bundle: %w", err)
	}
	defer func() { _ = out.Close() }()

	zw := zip.NewWriter(out)
	modified, err := time.Parse(time.RFC3339, manifest.GeneratedAt)
	if err != nil {
		return fmt.Errorf("export: parsing generatedAt: %w", err)
	}

	manifestJSON, err := marshalManifest(manifest)
	if err != nil {
		return err
	}
	if err := addZipEntry(zw, manifestPath, modified, func(w io.Writer) error {
		_, writeErr := w.Write(manifestJSON)
		return writeErr
	}); err != nil {
		return err
	}

	for _, entry := range staged {
		source := filepath.Join(stageDir, filepath.FromSlash(entry.Path))
		if err := addZipEntry(zw, entry.Path, modified, func(w io.Writer) error {
			src, openErr := os.Open(source)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = src.Close() }()
			_, copyErr := io.Copy(w, src)
			return copyErr
		}); err != nil {
			return err
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("export: finishing bundle: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("export: closing bundle: %w", err)
	}
	return nil
}

func addZipEntry(zw *zip.Writer, name string, modified time.Time, write func(io.Writer) error) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
	if err != nil {
		return fmt.Errorf("export: adding %s: %w", name, err)
	}
	if err := write(w); err != nil {
		return fmt.Errorf("export: writing %s: %w", name, err)
	}
	return nil
}

func marshalManifest(m Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("export: encoding manifest: %w", err)
	}
	return append(data, '\n'), nil
}

// resolveSections validates a requested subset against the registered writers
// and returns it in canonical order. An empty request means every section this
// producer can write.
//
// Both refusals are explicit rather than an empty section, because a section
// present with zero counts is a claim that the organization holds none of that
// data — which a typo or an unbuilt writer has no business making.
func (s *Service) resolveSections(requested []string) ([]string, error) {
	registered := make(map[string]bool, len(s.writers))
	for _, w := range s.writers {
		registered[w.Key()] = true
	}

	if len(requested) == 0 {
		resolved := make([]string, 0, len(registered))
		for _, key := range SectionOrder {
			if registered[key] {
				resolved = append(resolved, key)
			}
		}
		return resolved, nil
	}

	seen := make(map[string]bool, len(requested))
	for _, key := range requested {
		if !slices.Contains(SectionOrder, key) {
			return nil, &respond.APIError{
				Status:  http.StatusBadRequest,
				Code:    "invalid_section",
				Message: fmt.Sprintf("unknown export section %q", key),
			}
		}
		if !registered[key] {
			return nil, &respond.APIError{
				Status:  http.StatusBadRequest,
				Code:    "section_unavailable",
				Message: fmt.Sprintf("export section %q is not available yet", key),
			}
		}
		seen[key] = true
	}
	resolved := make([]string, 0, len(seen))
	for _, key := range SectionOrder {
		if seen[key] {
			resolved = append(resolved, key)
		}
	}
	return resolved, nil
}

// ParseSections reads the ?sections= filter.
func ParseSections(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	sections := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			sections = append(sections, trimmed)
		}
	}
	return sections
}

func filename(slug, generatedAt string) string {
	stamp := strings.NewReplacer(":", "", "-", "").Replace(generatedAt)
	return fmt.Sprintf("%s-export-%s.zip", slug, stamp)
}
