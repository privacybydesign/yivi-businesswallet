package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// ASiC-E (ETSI EN 319 162) packaging constants.
const (
	asiceMediaType = "application/vnd.etsi.asic-e+zip"
	asiceExtension = ".asice"

	// mimetypeEntry must be the container's first entry and stored uncompressed,
	// so a reader can identify the container from its first bytes without
	// inflating anything. That placement is the format's one hard rule and the
	// reason this package writes the ZIP by hand rather than reusing Bundle.
	mimetypeEntry = "mimetype"

	// evidenceIndexEntry describes the raw blobs beside it, so a container still
	// makes sense after someone lifts it out of the bundle.
	evidenceIndexEntry = "evidence-index.json"

	evidenceDir = "evidence/"
)

// evidenceBlob is one raw evidence record on its way into a container.
type evidenceBlob struct {
	ID                 string
	Type               string
	ProviderRef        string
	QualifiedTimestamp time.Time
	Raw                []byte
}

// evidenceIndex is the container's own manifest.
type evidenceIndex struct {
	MessageID string                `json:"messageId"`
	Records   []evidenceIndexRecord `json:"records"`
}

type evidenceIndexRecord struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	ProviderRef        string   `json:"providerRef,omitempty"`
	QualifiedTimestamp string   `json:"qualifiedTimestamp"`
	Path               string   `json:"path"`
	MediaType          string   `json:"mediaType"`
	SizeBytes          int64    `json:"sizeBytes"`
	Checksum           Checksum `json:"checksum"`
}

// buildASiCE packages one message's evidence: the raw blobs verbatim plus an
// index binding each to its qualified timestamp. We add no signature of our own
// — the qualified timestamps inside are the QTSP's, and packaging evidence is
// not sealing it.
func buildASiCE(messageID string, blobs []evidenceBlob, modified time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := writeMimetype(zw); err != nil {
		return nil, err
	}

	index := evidenceIndex{MessageID: messageID, Records: make([]evidenceIndexRecord, 0, len(blobs))}
	for _, blob := range blobs {
		path := evidenceDir + blob.ID
		w, err := zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return nil, fmt.Errorf("export: adding evidence %s: %w", blob.ID, err)
		}
		// Raw ERDS evidence is what gives a message legal effect, so the bytes go
		// in untouched: not re-encoded, not re-indented, no size threshold.
		if _, err := w.Write(blob.Raw); err != nil {
			return nil, fmt.Errorf("export: writing evidence %s: %w", blob.ID, err)
		}
		index.Records = append(index.Records, evidenceIndexRecord{
			ID:                 blob.ID,
			Type:               blob.Type,
			ProviderRef:        blob.ProviderRef,
			QualifiedTimestamp: timestamp(blob.QualifiedTimestamp),
			Path:               path,
			MediaType:          evidenceMediaType(blob.Raw),
			SizeBytes:          int64(len(blob.Raw)),
			Checksum:           checksumOf(blob.Raw),
		})
	}

	indexJSON, err := json.Marshal(index)
	if err != nil {
		return nil, fmt.Errorf("export: encoding evidence index: %w", err)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: evidenceIndexEntry, Method: zip.Deflate, Modified: modified})
	if err != nil {
		return nil, fmt.Errorf("export: adding evidence index: %w", err)
	}
	if _, err := w.Write(append(indexJSON, '\n')); err != nil {
		return nil, fmt.Errorf("export: writing evidence index: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("export: finishing evidence container: %w", err)
	}
	return buf.Bytes(), nil
}

// writeMimetype writes the identifying first entry. It is stored rather than
// deflated and carries no modification time, so the container's leading bytes
// are the same in every ASiC-E ever written.
func writeMimetype(zw *zip.Writer) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: mimetypeEntry, Method: zip.Store})
	if err != nil {
		return fmt.Errorf("export: adding %s: %w", mimetypeEntry, err)
	}
	if _, err := w.Write([]byte(asiceMediaType)); err != nil {
		return fmt.Errorf("export: writing %s: %w", mimetypeEntry, err)
	}
	return nil
}

// evidenceMediaType reports what the blob looks like. The QTSP declares no type
// with the bytes and ETSI EN 319 522-3 evidence is XML in practice, so the type
// is sniffed rather than assumed — and anything unrecognised stays opaque.
func evidenceMediaType(raw []byte) string {
	if head := bytes.TrimSpace(raw); bytes.HasPrefix(head, []byte("<")) {
		return "application/xml"
	}
	return defaultMediaType
}
