package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const unlimitedBudget int64 = 0

const jsonMediaType = "application/json"

// Bundle stages a bundle's files on disk. Staging rather than streaming is
// forced by the layout: manifest.json is the first ZIP entry but carries the
// checksum of every entry after it, so nothing can be written until everything
// is known.
type Bundle struct {
	dir      string
	budget   int64
	consumed int64
}

func newBundle(dir string, budget int64) *Bundle {
	return &Bundle{dir: dir, budget: budget}
}

func (b *Bundle) section(key, subdir string) *SectionBundle {
	return &SectionBundle{
		bundle: b,
		key:    key,
		subdir: subdir,
		counts: make(map[string]int),
	}
}

func (b *Bundle) admits(size int64) bool {
	if b.budget == unlimitedBudget {
		b.consumed += size
		return true
	}
	if b.consumed+size > b.budget {
		return false
	}
	b.consumed += size
	return true
}

// SectionBundle is the handle a section writer writes through. Paths are
// relative to the section's own directory.
type SectionBundle struct {
	bundle  *Bundle
	key     string
	subdir  string
	counts  map[string]int
	files   []FileEntry
	omitted []Omission
}

// AddJSON writes a section index. An index is always carried, whatever the
// budget: it is the record, and the payloads it references are what become
// omissions.
func (s *SectionBundle) AddJSON(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("export: encoding %s/%s: %w", s.subdir, name, err)
	}
	return s.write(name, jsonMediaType, append(data, '\n'))
}

// AddBytes carries a payload verbatim. Over budget it becomes a reference-only
// record instead.
func (s *SectionBundle) AddBytes(name, mediaType string, data []byte) error {
	if err := validPath(s.path(name)); err != nil {
		return err
	}
	if !s.bundle.admits(int64(len(data))) {
		sum := sha256.Sum256(data)
		s.Omit(name, ReasonSizeLimit, int64(len(data)), &Checksum{
			Algorithm: checksumAlgorithm,
			Value:     hex.EncodeToString(sum[:]),
		})
		return nil
	}
	return s.write(name, mediaType, data)
}

// Count records how many rows of a named collection the section exported.
func (s *SectionBundle) Count(key string, n int) {
	s.counts[key] = n
}

// Omit records a payload the section references but does not carry. Pass a zero
// size and a nil checksum when the store could not tell.
func (s *SectionBundle) Omit(name, reason string, size int64, checksum *Checksum) {
	s.omitted = append(s.omitted, Omission{
		Path:      s.path(name),
		Reason:    reason,
		SizeBytes: size,
		Checksum:  checksum,
	})
}

func (s *SectionBundle) write(name, mediaType string, data []byte) error {
	bundlePath := s.path(name)
	if err := validPath(bundlePath); err != nil {
		return err
	}

	staged := filepath.Join(s.bundle.dir, filepath.FromSlash(bundlePath))
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return fmt.Errorf("export: staging %s: %w", bundlePath, err)
	}
	if err := os.WriteFile(staged, data, 0o600); err != nil {
		return fmt.Errorf("export: staging %s: %w", bundlePath, err)
	}

	sum := sha256.Sum256(data)
	s.files = append(s.files, FileEntry{
		Path:      bundlePath,
		MediaType: mediaType,
		SizeBytes: int64(len(data)),
		Checksum:  Checksum{Algorithm: checksumAlgorithm, Value: hex.EncodeToString(sum[:])},
	})
	return nil
}

func (s *SectionBundle) path(name string) string {
	return s.subdir + "/" + name
}

func (s *SectionBundle) manifest() Section {
	files := s.files
	if files == nil {
		files = []FileEntry{}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	omitted := s.omitted
	if omitted == nil {
		omitted = []Omission{}
	}
	sort.Slice(omitted, func(i, j int) bool { return omitted[i].Path < omitted[j].Path })

	return Section{
		Key:     s.key,
		Counts:  s.counts,
		Files:   files,
		Omitted: omitted,
	}
}

// validPath enforces the layout's path rules. A writer names files from stored
// data — an attachment uuid, a credential ref — so they are checked, not assumed.
func validPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("export: empty bundle path")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("export: absolute bundle path %q", p)
	case strings.Contains(p, `\`):
		return fmt.Errorf("export: backslash in bundle path %q", p)
	case p != path.Clean(p):
		return fmt.Errorf("export: unclean bundle path %q", p)
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("export: invalid segment in bundle path %q", p)
		}
	}
	for _, r := range p {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("export: non-ASCII bundle path %q", p)
		}
	}
	return nil
}
