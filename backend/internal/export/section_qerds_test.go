package export

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

type fakeQerds struct {
	messages  []qerds.Message
	details   map[uuid.UUID]qerds.MessageWithEvidence
	content   map[uuid.UUID][]byte
	addresses []qerds.Address
	contacts  []qerds.Contact
}

func (f *fakeQerds) List(context.Context, uuid.UUID) ([]qerds.Message, error) {
	return f.messages, nil
}

func (f *fakeQerds) GetWithEvidence(_ context.Context, _ uuid.UUID, id uuid.UUID) (qerds.MessageWithEvidence, error) {
	return f.details[id], nil
}

func (f *fakeQerds) GetAttachmentContent(_ context.Context, _, _ uuid.UUID, attachmentID uuid.UUID) (qerds.AttachmentContent, error) {
	content, ok := f.content[attachmentID]
	if !ok {
		return qerds.AttachmentContent{}, qerds.ErrAttachmentNotFound
	}
	return qerds.AttachmentContent{Content: content}, nil
}

func (f *fakeQerds) ListAddresses(context.Context, uuid.UUID) ([]qerds.Address, error) {
	return f.addresses, nil
}

func (f *fakeQerds) ListContacts(context.Context, uuid.UUID) ([]qerds.Contact, error) {
	return f.contacts, nil
}

func qerdsWriter(store qerdsReader) *QerdsWriter {
	w := NewQerdsWriter(store)
	w.now = func() time.Time { return time.Date(2026, 7, 27, 9, 14, 2, 0, time.UTC) }
	return w
}

// evidenceXML is the shape ETSI EN 319 522-3 evidence takes in practice.
const evidenceXML = `<?xml version="1.0"?><REMEvidence xmlns="http://uri.etsi.org/19522"><EventCode>Acceptance</EventCode></REMEvidence>`

func messageWithEvidence(t *testing.T) (*fakeQerds, uuid.UUID, uuid.UUID) {
	t.Helper()
	messageID := uuid.New()
	evidenceID := uuid.New()
	now := time.Date(2026, 6, 6, 6, 6, 6, 0, time.UTC)

	message := qerds.Message{
		ID: messageID, Direction: "outbound",
		SenderAddress: "qerds:nl:caesar", RecipientAddress: "qerds:nl:acme",
		Subject: "Invoice 2026-04", Body: "See attached.",
		ProviderRef: "qtsp-msg-8817", Status: "delivered",
		SubmittedAt: &now, DeliveredAt: &now, QualifiedTimestampSend: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	return &fakeQerds{
		messages: []qerds.Message{message},
		details: map[uuid.UUID]qerds.MessageWithEvidence{messageID: {
			Message: message,
			Evidence: []qerds.Evidence{{
				ID: evidenceID, MessageID: messageID, Type: "SubmissionAcceptanceRejection",
				ProviderRef: "qtsp-ev-1", QualifiedTimestamp: now,
				Raw: []byte(evidenceXML), CreatedAt: now,
			}},
		}},
	}, messageID, evidenceID
}

func TestQerdsWriterPackagesEvidenceAsASiCE(t *testing.T) {
	store, messageID, evidenceID := messageWithEvidence(t)

	entry, files := runSection(t, qerdsWriter(store))

	path := "qerds/evidence/" + messageID.String() + ".asice"
	container, ok := files[path]
	if !ok {
		t.Fatalf("no evidence container at %s (files: %v)", path, entry.Files)
	}
	for _, f := range entry.Files {
		if f.Path == path && f.MediaType != asiceMediaType {
			t.Errorf("mediaType = %q, want %q", f.MediaType, asiceMediaType)
		}
	}
	if entry.Counts["evidence"] != 1 || entry.Counts["messages"] != 1 {
		t.Errorf("counts = %v, want one message and one evidence record", entry.Counts)
	}

	zr, err := zip.NewReader(bytes.NewReader(container), int64(len(container)))
	if err != nil {
		t.Fatalf("container is not a readable zip: %v", err)
	}
	// A reader identifies an ASiC-E from its first entry without inflating
	// anything, so mimetype must come first and be stored.
	if zr.File[0].Name != mimetypeEntry {
		t.Errorf("first entry = %q, want %q", zr.File[0].Name, mimetypeEntry)
	}
	if zr.File[0].Method != zip.Store {
		t.Errorf("mimetype method = %d, want it stored uncompressed", zr.File[0].Method)
	}
	if got := readZipEntry(t, zr, mimetypeEntry); string(got) != asiceMediaType {
		t.Errorf("mimetype = %q, want %q", got, asiceMediaType)
	}

	// Raw evidence is what gives the message legal effect; anything but a
	// verbatim copy stops verifying.
	if got := readZipEntry(t, zr, "evidence/"+evidenceID.String()); string(got) != evidenceXML {
		t.Errorf("evidence bytes = %q, want them verbatim", got)
	}

	var index evidenceIndex
	decodeInto(t, readZipEntry(t, zr, evidenceIndexEntry), &index)
	if index.MessageID != messageID.String() || len(index.Records) != 1 {
		t.Fatalf("index = %+v, want one record for the message", index)
	}
	record := index.Records[0]
	if record.QualifiedTimestamp != "2026-06-06T06:06:06Z" {
		t.Errorf("qualifiedTimestamp = %q, want RFC 3339 UTC", record.QualifiedTimestamp)
	}
	if record.ProviderRef != "qtsp-ev-1" {
		t.Errorf("providerRef = %q, want the QTSP's handle", record.ProviderRef)
	}
	if record.MediaType != "application/xml" {
		t.Errorf("mediaType = %q, want application/xml for XML evidence", record.MediaType)
	}
	if record.Checksum != checksumOf([]byte(evidenceXML)) {
		t.Errorf("checksum = %+v, want a sha-256 over the raw bytes", record.Checksum)
	}

	var messages []messageRecord
	decodeInto(t, files["qerds/messages.json"], &messages)
	if messages[0].EvidencePath != path {
		t.Errorf("evidencePath = %q, want %q", messages[0].EvidencePath, path)
	}
	if messages[0].ProviderRef != "qtsp-msg-8817" {
		t.Errorf("providerRef = %q, want the QTSP's message handle", messages[0].ProviderRef)
	}
}

// A message with no evidence gets no container, and its record says so by
// carrying no path rather than pointing at an empty one.
func TestQerdsWriterWritesNoContainerWithoutEvidence(t *testing.T) {
	messageID := uuid.New()
	message := qerds.Message{ID: messageID, Direction: "inbound", Status: "received"}
	store := &fakeQerds{
		messages: []qerds.Message{message},
		details:  map[uuid.UUID]qerds.MessageWithEvidence{messageID: {Message: message}},
	}

	entry, files := runSection(t, qerdsWriter(store))

	for _, f := range entry.Files {
		if f.MediaType == asiceMediaType {
			t.Errorf("wrote a container for a message with no evidence: %s", f.Path)
		}
	}
	var messages []messageRecord
	decodeInto(t, files["qerds/messages.json"], &messages)
	if messages[0].EvidencePath != "" {
		t.Errorf("evidencePath = %q, want none", messages[0].EvidencePath)
	}
	if entry.Counts["evidence"] != 0 {
		t.Errorf("evidence count = %d, want 0", entry.Counts["evidence"])
	}
}

// Attachments are named by uuid, so a provider-supplied filename never reaches a
// path — that removes the traversal and duplicate-name class instead of
// sanitising it.
func TestQerdsWriterNamesAttachmentsByUUID(t *testing.T) {
	messageID, attachmentID := uuid.New(), uuid.New()
	payload := []byte("%PDF-1.7 invoice bytes")
	message := qerds.Message{ID: messageID, Direction: "outbound", Status: "sent"}
	store := &fakeQerds{
		messages: []qerds.Message{message},
		details: map[uuid.UUID]qerds.MessageWithEvidence{messageID: {
			Message: message,
			Attachments: []qerds.Attachment{{
				ID: attachmentID, MessageID: messageID,
				Filename:    "../../etc/passwd",
				ContentType: "application/pdf",
				ContentHash: "9f2b1c" + "0000000000000000000000000000000000000000000000000000000000"[:58],
				SizeBytes:   int64(len(payload)),
			}},
		}},
		content: map[uuid.UUID][]byte{attachmentID: payload},
	}

	entry, files := runSection(t, qerdsWriter(store))

	path := "qerds/attachments/" + messageID.String() + "/" + attachmentID.String()
	if got := files[path]; string(got) != string(payload) {
		t.Errorf("attachment bytes = %q, want the stored payload", got)
	}
	for _, f := range entry.Files {
		if strings.Contains(f.Path, "passwd") || strings.Contains(f.Path, "..") {
			t.Errorf("the provider filename reached a bundle path: %s", f.Path)
		}
	}

	var messages []messageRecord
	decodeInto(t, files["qerds/messages.json"], &messages)
	att := messages[0].Attachments[0]
	if att.Path != path {
		t.Errorf("path = %q, want %q", att.Path, path)
	}
	// The original name is data, and it belongs in the record rather than on disk.
	if att.Filename != "../../etc/passwd" {
		t.Errorf("filename = %q, want the original preserved in the record", att.Filename)
	}
	if entry.Counts["attachments"] != 1 {
		t.Errorf("attachments count = %d, want 1", entry.Counts["attachments"])
	}
}

// One payload the store cannot return must not fail the export: the record and
// its stored hash are still the org's evidence that the attachment existed.
func TestQerdsWriterOmitsUnavailableAttachments(t *testing.T) {
	messageID, attachmentID := uuid.New(), uuid.New()
	hash := "e0b1" + "00000000000000000000000000000000000000000000000000000000000000"[:60]
	message := qerds.Message{ID: messageID, Direction: "inbound", Status: "received"}
	store := &fakeQerds{
		messages: []qerds.Message{message},
		details: map[uuid.UUID]qerds.MessageWithEvidence{messageID: {
			Message: message,
			Attachments: []qerds.Attachment{{
				ID: attachmentID, MessageID: messageID, Filename: "big.zip",
				ContentHash: hash, SizeBytes: 734003200,
			}},
		}},
	}

	entry, files := runSection(t, qerdsWriter(store))

	if len(entry.Omitted) != 1 {
		t.Fatalf("omitted = %+v, want the missing attachment", entry.Omitted)
	}
	omission := entry.Omitted[0]
	if omission.Reason != ReasonUnavailable || omission.SizeBytes != 734003200 {
		t.Errorf("omission = %+v, want unavailable with the stored size", omission)
	}
	if omission.Checksum == nil || omission.Checksum.Value != hash {
		t.Errorf("omitted checksum = %+v, want the stored hash", omission.Checksum)
	}
	if entry.Counts["attachments"] != 0 {
		t.Errorf("attachments count = %d, want 0 carried", entry.Counts["attachments"])
	}

	var messages []messageRecord
	decodeInto(t, files["qerds/messages.json"], &messages)
	if messages[0].Attachments[0].Path != "" {
		t.Errorf("path = %q, want none when nothing was carried", messages[0].Attachments[0].Path)
	}
}

// Evidence is carried whatever the budget: it is what gives a message legal
// effect, and dropping it to save space is not a trade this export makes.
func TestQerdsWriterCarriesEvidenceOverBudget(t *testing.T) {
	store, messageID, _ := messageWithEvidence(t)
	dir := t.TempDir()
	section := newBundle(dir, 1).section(SectionQerds, sectionDirs[SectionQerds])

	if err := qerdsWriter(store).Write(context.Background(), testOrg().ID, section); err != nil {
		t.Fatalf("Write() = %v, want nil", err)
	}

	entry := section.manifest()
	want := "qerds/evidence/" + messageID.String() + ".asice"
	var found bool
	for _, f := range entry.Files {
		if f.Path == want {
			found = true
		}
	}
	if !found {
		t.Errorf("the evidence container was dropped over budget: %+v", entry.Files)
	}
	for _, o := range entry.Omitted {
		if o.Path == want {
			t.Errorf("the evidence container was omitted: %+v", o)
		}
	}
}

func TestQerdsWriterExportsAddressesAndContacts(t *testing.T) {
	legal := "Acme B.V."
	store := &fakeQerds{
		addresses: []qerds.Address{{ID: uuid.New(), Address: "qerds:nl:caesar", IsDefault: true, ProviderRef: "qtsp-addr-3"}},
		contacts:  []qerds.Contact{{ID: uuid.New(), Name: "Acme", Address: "qerds:nl:acme", LegalName: &legal}},
	}

	entry, files := runSection(t, qerdsWriter(store))

	if entry.Counts["addresses"] != 1 || entry.Counts["contacts"] != 1 {
		t.Errorf("counts = %v, want one address and one contact", entry.Counts)
	}
	var addresses []addressRecord
	decodeInto(t, files["qerds/addresses.json"], &addresses)
	if !addresses[0].IsDefault || addresses[0].ProviderRef != "qtsp-addr-3" {
		t.Errorf("address = %+v, want the default flag and provider reference", addresses[0])
	}
	var contacts []contactRecord
	decodeInto(t, files["qerds/contacts.json"], &contacts)
	if contacts[0].LegalName == nil || *contacts[0].LegalName != legal {
		t.Errorf("contact = %+v, want the legal name", contacts[0])
	}
}

// An omission with a mislabelled digest is worse than one with none, so a stored
// hash that is not a bare sha-256 is reported as unknown.
func TestStoredChecksumRejectsNonSHA256Hashes(t *testing.T) {
	cases := map[string]string{
		"empty":     "",
		"prefixed":  "sha256:9f2b",
		"too short": "9f2b",
		"uppercase": strings.Repeat("A", 64),
	}
	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			if got := storedChecksum(hash); got != nil {
				t.Errorf("storedChecksum(%q) = %+v, want nil", hash, got)
			}
		})
	}
	valid := strings.Repeat("a1b2", 16)
	if got := storedChecksum(valid); got == nil || got.Value != valid {
		t.Errorf("storedChecksum(%q) = %+v, want it carried", valid, got)
	}
}

func readZipEntry(t *testing.T, zr *zip.Reader, name string) []byte {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s: %v", name, err)
		}
		defer func() { _ = rc.Close() }()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return data
	}
	t.Fatalf("entry %q not in container", name)
	return nil
}
