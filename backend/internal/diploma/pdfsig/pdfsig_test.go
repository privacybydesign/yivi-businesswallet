package pdfsig

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// contentsHexWidth is the reserved width of the /Contents hex string, as a
// signing tool reserves it before it knows the signature's length.
const contentsHexWidth = 64

// signedPDF lays out a one-signature PDF the way a signing tool does: the
// /Contents hex string is the gap between the two ByteRange segments, and the
// zero-padded ByteRange follows it in the second segment.
func signedPDF(cms []byte) []byte {
	prefix := "%PDF-1.7\n1 0 obj\n<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /ETSI.CAdES.detached " +
		"/M (D:20240102030405Z) /Reference [<< /TransformMethod /DocMDP /TransformParams << /P 1 >> >>] /Contents "
	contents := "<" + hex.EncodeToString(cms) + strings.Repeat("0", contentsHexWidth-2*len(cms)) + ">"
	suffixFormat := " /ByteRange [%010d %010d %010d %010d] >>\nendobj\n%%%%EOF\n"
	suffixLen := len(fmt.Sprintf(suffixFormat, 0, 0, 0, 0))
	secondStart := len(prefix) + len(contents)
	suffix := fmt.Sprintf(suffixFormat, 0, len(prefix), secondStart, suffixLen)
	return []byte(prefix + contents + suffix)
}

func TestExtract(t *testing.T) {
	cms := []byte{0x30, 0x04, 0x04, 0x02, 0x01, 0x00}
	pdf := signedPDF(cms)

	sigs, err := Extract(pdf)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("Extract found %d signatures, want 1", len(sigs))
	}
	sig := sigs[0]
	if !bytes.Equal(sig.CMS, cms) {
		t.Errorf("CMS = %x, want %x without the zero padding", sig.CMS, cms)
	}
	if sig.SubFilter != "ETSI.CAdES.detached" || sig.Filter != "Adobe.PPKLite" {
		t.Errorf("SubFilter, Filter = %q, %q", sig.SubFilter, sig.Filter)
	}
	if sig.SigningTimeClaimed != "D:20240102030405Z" || sig.DocMDPPermission != 1 {
		t.Errorf("M, DocMDP = %q, %d", sig.SigningTimeClaimed, sig.DocMDPPermission)
	}
	if !sig.CoversWholeFile {
		t.Error("CoversWholeFile = false for an unmodified file")
	}

	signed, err := sig.SignedBytes(pdf)
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}
	gapStart, gapEnd := sig.ByteRange[1], sig.ByteRange[2]
	want := append(append([]byte{}, pdf[:gapStart]...), pdf[gapEnd:]...)
	if !bytes.Equal(signed, want) {
		t.Error("SignedBytes is not the file without its /Contents string")
	}

	// An incremental update after signing leaves the appended bytes unsigned.
	updated := append(append([]byte{}, pdf...), []byte("2 0 obj\n<< >>\nendobj\n")...)
	sigs, err = Extract(updated)
	if err != nil {
		t.Fatalf("Extract updated: %v", err)
	}
	if sigs[0].CoversWholeFile {
		t.Error("CoversWholeFile = true for a file with bytes appended after signing")
	}
}

func TestExtractRejects(t *testing.T) {
	cases := map[string]string{
		"not a PDF":    "hello",
		"no signature": "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n",
		// Each entry fits an int64 on its own, but offset+length wraps negative.
		"overflowing range":  "%PDF-1.7\n/ByteRange [1 9223372036854775807 2 3]",
		"range past the end": "%PDF-1.7\n/ByteRange [0 5 4000 10]",
		"entry past int64":   "%PDF-1.7\n/ByteRange [0 99999999999999999999 4 5]",
		"no hex contents":    "%PDF-1.7\n(abc) /ByteRange [0 9 14 30]",
	}
	for name, pdf := range cases {
		t.Run(name, func(t *testing.T) {
			if sigs, err := Extract([]byte(pdf)); err == nil {
				t.Errorf("Extract = %+v, want an error", sigs)
			}
		})
	}
}

func TestSignedBytesOverflow(t *testing.T) {
	pdf := []byte("%PDF-1.7\n")
	for _, br := range [][4]int64{
		{1, 1<<63 - 1, 2, 3},
		{0, 2, 1<<63 - 1, 1<<63 - 1},
		{0, 4, 2, 3},
	} {
		sig := Signature{ByteRange: br}
		if _, err := sig.SignedBytes(pdf); err == nil {
			t.Errorf("SignedBytes(%v) succeeded, want an error", br)
		}
	}
}
