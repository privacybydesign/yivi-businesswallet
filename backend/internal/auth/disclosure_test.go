package auth

import (
	"errors"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func presentation(claims map[string]string) openid4vpverifier.Presentation {
	return openid4vpverifier.Presentation{Claims: claims}
}

func TestExtractEmail(t *testing.T) {
	tests := []struct {
		name      string
		claims    map[string]string
		wantEmail user.Email
		wantErr   error
	}{
		{
			name:      "valid disclosure yields email",
			claims:    map[string]string{openid4vpverifier.ClaimEmail: "user@example.test"},
			wantEmail: "user@example.test",
		},
		{
			name:      "mixed-case disclosure is normalized",
			claims:    map[string]string{openid4vpverifier.ClaimEmail: "  User@Example.TEST  "},
			wantEmail: "user@example.test",
		},
		{
			name:    "missing email -> disclosure invalid",
			claims:  map[string]string{},
			wantErr: errDisclosureInvalid,
		},
		{
			name:    "empty email -> disclosure invalid",
			claims:  map[string]string{openid4vpverifier.ClaimEmail: ""},
			wantErr: errDisclosureInvalid,
		},
		{
			name:    "malformed email -> disclosure invalid",
			claims:  map[string]string{openid4vpverifier.ClaimEmail: "nope"},
			wantErr: errDisclosureInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, err := extractEmail(presentation(tt.claims))
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

func TestExtractIdentity(t *testing.T) {
	tests := []struct {
		name    string
		claims  map[string]string
		want    DisclosedIdentity
		wantErr error
	}{
		{
			name: "full disclosure yields identity",
			claims: map[string]string{
				openid4vpverifier.ClaimGivenNames: "José",
				openid4vpverifier.ClaimFamilyName: "van der Berg",
				openid4vpverifier.ClaimEmail:      "user@example.test",
			},
			want: DisclosedIdentity{Email: "user@example.test", Name: identity.Name{GivenNames: "José", LastName: "van der Berg"}},
		},
		{
			name: "date of birth and phone are carried through",
			claims: map[string]string{
				openid4vpverifier.ClaimGivenNames:  "José",
				openid4vpverifier.ClaimFamilyName:  "van der Berg",
				openid4vpverifier.ClaimEmail:       "user@example.test",
				openid4vpverifier.ClaimDateOfBirth: " 1980-01-02 ",
				openid4vpverifier.ClaimPhone:       "+31600000000",
			},
			want: DisclosedIdentity{Email: "user@example.test", Name: identity.Name{GivenNames: "José", LastName: "van der Berg"}, DateOfBirth: "1980-01-02", Phone: "+31600000000"},
		},
		{
			name: "name is kept literal, only trimmed",
			claims: map[string]string{
				openid4vpverifier.ClaimGivenNames: "  JOSE ",
				openid4vpverifier.ClaimFamilyName: "VAN DER BERG",
				openid4vpverifier.ClaimEmail:      "user@example.test",
			},
			want: DisclosedIdentity{Email: "user@example.test", Name: identity.Name{GivenNames: "JOSE", LastName: "VAN DER BERG"}},
		},
		{
			name: "missing family name -> invalid",
			claims: map[string]string{
				openid4vpverifier.ClaimGivenNames: "José",
				openid4vpverifier.ClaimEmail:      "user@example.test",
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "missing given names -> invalid",
			claims: map[string]string{
				openid4vpverifier.ClaimFamilyName: "Berg",
				openid4vpverifier.ClaimEmail:      "user@example.test",
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "invalid email -> invalid",
			claims: map[string]string{
				openid4vpverifier.ClaimGivenNames: "José",
				openid4vpverifier.ClaimFamilyName: "Berg",
				openid4vpverifier.ClaimEmail:      "nope",
			},
			wantErr: errDisclosureInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractIdentity(presentation(tt.claims))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Fatalf("identity = %+v, want %+v", got, tt.want)
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
