// Package pdfsig extracts digital signatures from a PDF file.
//
// It deliberately avoids a full PDF parser: for verification purposes we only
// need the /ByteRange array, the /Contents hex string it points at, and a few
// scalar entries of the signature dictionary. Working on the raw bytes also
// keeps the "what exactly was signed" question honest: the signed bytes are
// simply the two ranges of the file that ByteRange names.
package pdfsig

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Signature is one signature found in a PDF.
type Signature struct {
	// ByteRange is the [offset length offset length] array from the /Sig dict.
	ByteRange [4]int64
	// CMS is the DER-encoded CMS SignedData that was stored in /Contents.
	CMS []byte
	// SubFilter is e.g. "ETSI.CAdES.detached" or "adbe.pkcs7.detached".
	SubFilter string
	// Filter is the signature handler, e.g. "Adobe.PPKLite".
	Filter string
	// SigningTimeClaimed is the raw /M entry (a PDF date string), if present.
	// It is asserted by the signer and NOT trustworthy; prefer the timestamp.
	SigningTimeClaimed string
	// DocMDPPermission is the /P value of a certification (DocMDP) signature:
	// 1 = no changes allowed, 2 = form fill-in allowed, 3 = annotations too.
	// 0 means this is not a certification signature.
	DocMDPPermission int
	// CoversWholeFile is true when the ByteRange spans the entire file except
	// the /Contents string itself. If false, the file has been modified
	// (incremental update) after signing and the extra content is unsigned.
	CoversWholeFile bool
}

// SignedBytes returns the bytes covered by the signature, i.e. the
// concatenation of the two ByteRange segments.
func (s *Signature) SignedBytes(pdf []byte) ([]byte, error) {
	br := s.ByteRange
	end1, end2 := br[0]+br[1], br[2]+br[3]
	if br[0] < 0 || br[2] < 0 || end1 > int64(len(pdf)) || end2 > int64(len(pdf)) || end1 > br[2] {
		return nil, fmt.Errorf("pdfsig: ByteRange %v does not fit in a %d byte file", br, len(pdf))
	}
	out := make([]byte, 0, br[1]+br[3])
	out = append(out, pdf[br[0]:end1]...)
	out = append(out, pdf[br[2]:end2]...)
	return out, nil
}

// Info holds document metadata taken from the PDF /Info dictionary.
type Info struct {
	Title, Author, Subject, Producer string
}

var (
	reByteRange = regexp.MustCompile(`/ByteRange\s*\[\s*(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*\]`)
	reTypeSig   = regexp.MustCompile(`/Type\s*/Sig\b`)
	reSubFilter = regexp.MustCompile(`/SubFilter\s*/([A-Za-z0-9.]+)`)
	reFilter    = regexp.MustCompile(`/Filter\s*/([A-Za-z0-9.]+)`)
	reM         = regexp.MustCompile(`/M\s*\(([^)]*)\)`)
	reDocMDP    = regexp.MustCompile(`(?s)/TransformMethod\s*/DocMDP.*?/P\s+(\d)`)
	reInfo      = map[string]*regexp.Regexp{
		"Title":    regexp.MustCompile(`/Title\s*\(((?:\\.|[^\\)])*)\)`),
		"Author":   regexp.MustCompile(`/Author\s*\(((?:\\.|[^\\)])*)\)`),
		"Subject":  regexp.MustCompile(`/Subject\s*\(((?:\\.|[^\\)])*)\)`),
		"Producer": regexp.MustCompile(`/Producer\s*\(((?:\\.|[^\\)])*)\)`),
	}
)

// Extract finds all signatures in the PDF.
func Extract(pdf []byte) ([]Signature, error) {
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, errors.New("pdfsig: not a PDF file")
	}
	var sigs []Signature
	for _, m := range reByteRange.FindAllSubmatchIndex(pdf, -1) {
		var s Signature
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseInt(string(pdf[m[2+2*i]:m[3+2*i]]), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("pdfsig: bad ByteRange: %w", err)
			}
			s.ByteRange[i] = v
		}
		// /Contents sits in the gap between the two ranges: <hex...>
		gapStart, gapEnd := s.ByteRange[0]+s.ByteRange[1], s.ByteRange[2]
		if gapStart >= gapEnd || gapEnd > int64(len(pdf)) {
			return nil, fmt.Errorf("pdfsig: ByteRange %v leaves no room for /Contents", s.ByteRange)
		}
		gap := bytes.TrimSpace(pdf[gapStart:gapEnd])
		if len(gap) < 2 || gap[0] != '<' || gap[len(gap)-1] != '>' {
			return nil, fmt.Errorf("pdfsig: /Contents at %d..%d is not a hex string", gapStart, gapEnd)
		}
		hx := bytes.TrimRight(gap[1:len(gap)-1], "0") // strip zero padding
		if len(hx)%2 == 1 {
			hx = append(hx, '0')
		}
		der, err := hex.DecodeString(string(hx))
		if err != nil {
			return nil, fmt.Errorf("pdfsig: /Contents is not valid hex: %w", err)
		}
		s.CMS = der
		s.CoversWholeFile = s.ByteRange[0] == 0 && s.ByteRange[2]+s.ByteRange[3] == int64(len(pdf))

		// The rest of the /Sig dictionary precedes /Contents (the hex string is
		// conventionally the last entry before /ByteRange). Look at the bytes
		// between the start of the dictionary and /Contents.
		dict := pdf[max(0, gapStart-4000):gapStart]
		if locs := reTypeSig.FindAllIndex(dict, -1); locs != nil {
			dict = dict[locs[len(locs)-1][0]:]
		}
		if mm := reSubFilter.FindSubmatch(dict); mm != nil {
			s.SubFilter = string(mm[1])
		}
		if mm := reFilter.FindSubmatch(dict); mm != nil {
			s.Filter = string(mm[1])
		}
		if mm := reM.FindSubmatch(dict); mm != nil {
			s.SigningTimeClaimed = string(mm[1])
		}
		if mm := reDocMDP.FindSubmatch(dict); mm != nil {
			s.DocMDPPermission, _ = strconv.Atoi(string(mm[1]))
		}
		sigs = append(sigs, s)
	}
	if len(sigs) == 0 {
		return nil, errors.New("pdfsig: no digital signature found in PDF")
	}
	return sigs, nil
}

// ExtractInfo reads a few /Info entries. Only literal strings are handled,
// which is all the DUO generator emits.
func ExtractInfo(pdf []byte) Info {
	get := func(k string) string {
		if mm := reInfo[k].FindSubmatch(pdf); mm != nil {
			return unescape(mm[1])
		}
		return ""
	}
	return Info{Title: get("Title"), Author: get("Author"), Subject: get("Subject"), Producer: get("Producer")}
}

func unescape(b []byte) string {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			i++
			switch b[i] {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			default:
				out = append(out, b[i])
			}
			continue
		}
		out = append(out, b[i])
	}
	return string(out)
}
