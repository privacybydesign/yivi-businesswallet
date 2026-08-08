package signing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckTurn(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	signers := func(states ...string) []Signer {
		ids := []uuid.UUID{a, b, c}
		out := make([]Signer, len(states))
		for i, st := range states {
			out[i] = Signer{UserID: ids[i], Order: i + 1, Status: st}
		}
		return out
	}

	tests := []struct {
		name    string
		mode    string
		signers []Signer
		user    uuid.UUID
		want    error
	}{
		{"parallel first can sign", ModeParallel, signers(SignerPending, SignerPending), a, nil},
		{"parallel second can sign before first", ModeParallel, signers(SignerPending, SignerPending), b, nil},
		{"parallel already signed", ModeParallel, signers(SignerSigned, SignerPending), a, ErrAlreadySigned},
		{"not a signer", ModeParallel, signers(SignerPending, SignerPending), c, ErrNotSigner},
		{"sequential first can sign", ModeSequential, signers(SignerPending, SignerPending), a, nil},
		{"sequential second must wait", ModeSequential, signers(SignerPending, SignerPending), b, ErrNotYourTurn},
		{"sequential second after first signed", ModeSequential, signers(SignerSigned, SignerPending), b, nil},
		{"sequential already signed", ModeSequential, signers(SignerSigned, SignerPending), a, ErrAlreadySigned},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Mode: tc.mode, Signers: tc.signers}
			if got := checkTurn(req, tc.user); !errors.Is(got, tc.want) {
				t.Fatalf("checkTurn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateRecipient(t *testing.T) {
	tests := []struct {
		name string
		rec  RecipientInput
		want error
	}{
		{"none", RecipientInput{Channel: ChannelNone}, nil},
		{"email with address", RecipientInput{Channel: ChannelEmail, Address: "a@example.org"}, nil},
		{"qerds with address", RecipientInput{Channel: ChannelQERDS, Address: "0208:nl:acme"}, nil},
		{"email without address", RecipientInput{Channel: ChannelEmail}, ErrInvalidRequest},
		{"qerds blank address", RecipientInput{Channel: ChannelQERDS, Address: "   "}, ErrInvalidRequest},
		{"unknown channel", RecipientInput{Channel: "sms", Address: "x"}, ErrInvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRecipient(tc.rec); !errors.Is(got, tc.want) {
				t.Fatalf("validateRecipient = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInvolves(t *testing.T) {
	creator, signer, stranger := uuid.New(), uuid.New(), uuid.New()
	req := Request{CreatedBy: creator, Signers: []Signer{{UserID: signer}}}
	if !involves(req, creator) {
		t.Fatal("creator should be involved")
	}
	if !involves(req, signer) {
		t.Fatal("signer should be involved")
	}
	if involves(req, stranger) {
		t.Fatal("stranger should not be involved")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.New()
	cur := encodeCursor(time.Unix(1_700_000_000, 123).UTC(), id)
	gotTime, gotID, has, err := decodeCursor(cur)
	if err != nil || !has {
		t.Fatalf("decodeCursor(%q) = has %v, err %v", cur, has, err)
	}
	if gotID != id {
		t.Fatalf("cursor id round-trip: got %s want %s", gotID, id)
	}
	if gotTime.IsZero() {
		t.Fatal("cursor time round-trip lost the timestamp")
	}
	if _, _, has, _ := decodeCursor(""); has {
		t.Fatal("empty cursor should report no cursor")
	}
	if _, _, _, err := decodeCursor("!!!not-base64!!!"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad cursor should be ErrInvalidRequest, got %v", err)
	}
}
