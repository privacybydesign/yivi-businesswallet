package auth

import (
	"errors"
	"net/http"
	"testing"

	irma "github.com/privacybydesign/irmago/irma"
	irmaserver "github.com/privacybydesign/irmago/irma/server"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const testEmailAttr = "irma-demo.sidn-pbdf.email.email"

func strptr(s string) *string { return &s }

func disclosed(id irma.AttributeTypeIdentifier, status irma.AttributeProofStatus, raw *string) [][]*irma.DisclosedAttribute {
	return [][]*irma.DisclosedAttribute{{{
		Identifier: id,
		Status:     status,
		RawValue:   raw,
	}}}
}

func TestExtractEmail(t *testing.T) {
	want := irma.NewAttributeTypeIdentifier(testEmailAttr)
	other := irma.NewAttributeTypeIdentifier("irma-demo.sidn-pbdf.email.domain")

	tests := []struct {
		name      string
		result    *irmaserver.SessionResult
		wantEmail string
		wantErr   error
	}{
		{
			name: "valid disclosure yields email",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   disclosed(want, irma.AttributeProofStatusPresent, strptr("user@example.test")),
			},
			wantEmail: "user@example.test",
		},
		{
			name:    "not done -> session not finished",
			result:  &irmaserver.SessionResult{Status: irma.ServerStatusConnected},
			wantErr: errSessionNotFinished,
		},
		{
			name:    "cancelled -> session not finished",
			result:  &irmaserver.SessionResult{Status: irma.ServerStatusCancelled},
			wantErr: errSessionNotFinished,
		},
		{
			name:    "timeout -> session not finished",
			result:  &irmaserver.SessionResult{Status: irma.ServerStatusTimeout},
			wantErr: errSessionNotFinished,
		},
		{
			name: "done but proof invalid -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusInvalid,
				Disclosed:   disclosed(want, irma.AttributeProofStatusPresent, strptr("user@example.test")),
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "empty disclosed -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   [][]*irma.DisclosedAttribute{},
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "wrong attribute identifier -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   disclosed(other, irma.AttributeProofStatusPresent, strptr("user@example.test")),
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "attribute not present -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   disclosed(want, irma.AttributeProofStatusNull, strptr("user@example.test")),
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "nil raw value -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   disclosed(want, irma.AttributeProofStatusPresent, nil),
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "empty raw value -> disclosure invalid",
			result: &irmaserver.SessionResult{
				Status:      irma.ServerStatusDone,
				ProofStatus: irma.ProofStatusValid,
				Disclosed:   disclosed(want, irma.AttributeProofStatusPresent, strptr("")),
			},
			wantErr: errDisclosureInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, err := extractEmail(tt.result, want)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if email != tt.wantEmail {
				t.Fatalf("email = %q, want %q", email, tt.wantEmail)
			}
		})
	}
}

func TestMapClaimError(t *testing.T) {
	tests := []struct {
		name       string
		in         error
		wantStatus int
		wantCode   string
		wantAPI    bool
	}{
		{"not finished -> 409", errSessionNotFinished, http.StatusConflict, "session_not_finished", true},
		{"invalid -> 422", errDisclosureInvalid, http.StatusUnprocessableEntity, "disclosure_invalid", true},
		{"not invited -> 403", errUserNotInvited, http.StatusForbidden, "user_not_invited", true},
		{"unexpected -> passthrough (not APIError)", errors.New("boom"), 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapClaimError(tt.in)
			var apiErr *respond.APIError
			if errors.As(got, &apiErr) {
				if !tt.wantAPI {
					t.Fatalf("expected non-APIError passthrough, got APIError %+v", apiErr)
				}
				if apiErr.Status != tt.wantStatus || apiErr.Code != tt.wantCode {
					t.Fatalf("got status=%d code=%q, want status=%d code=%q",
						apiErr.Status, apiErr.Code, tt.wantStatus, tt.wantCode)
				}
				return
			}
			if tt.wantAPI {
				t.Fatalf("expected APIError, got %v", got)
			}
			if !errors.Is(got, tt.in) {
				t.Fatalf("passthrough should preserve original error, got %v", got)
			}
		})
	}
}
