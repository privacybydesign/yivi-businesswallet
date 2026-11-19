package auth

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

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

func TestExtractVog(t *testing.T) {
	tests := []struct {
		name    string
		claims  map[string]string
		want    DisclosedVog
		wantErr error
	}{
		{
			name: "full disclosure yields identity and requested aspects",
			claims: map[string]string{
				openid4vpverifier.ClaimVogGivenNames:   "José",
				openid4vpverifier.ClaimVogSurname:      "Berg",
				openid4vpverifier.ClaimVogPrefix:       "van der",
				openid4vpverifier.ClaimVogDateOfBirth:  "1980-01-02",
				openid4vpverifier.ClaimVogIssueDate:    "2024-05-01",
				openid4vpverifier.VogAspectClaim("11"): "yes",
				openid4vpverifier.VogAspectClaim("22"): "no",
			},
			want: DisclosedVog{
				GivenNames:  "José",
				Surname:     "van der Berg",
				DateOfBirth: "1980-01-02",
				IssueDate:   time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC),
				AspectCodes: []string{"11"},
			},
		},
		{
			name: "empty surname with non-empty prefix -> invalid, not a garbled name",
			claims: map[string]string{
				openid4vpverifier.ClaimVogGivenNames:  "José",
				openid4vpverifier.ClaimVogSurname:     "",
				openid4vpverifier.ClaimVogPrefix:      "van der",
				openid4vpverifier.ClaimVogDateOfBirth: "1980-01-02",
				openid4vpverifier.ClaimVogIssueDate:   "2024-05-01",
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "missing given names -> invalid",
			claims: map[string]string{
				openid4vpverifier.ClaimVogSurname:     "Berg",
				openid4vpverifier.ClaimVogDateOfBirth: "1980-01-02",
				openid4vpverifier.ClaimVogIssueDate:   "2024-05-01",
			},
			wantErr: errDisclosureInvalid,
		},
		{
			name: "malformed issue date -> invalid",
			claims: map[string]string{
				openid4vpverifier.ClaimVogGivenNames:  "José",
				openid4vpverifier.ClaimVogSurname:     "Berg",
				openid4vpverifier.ClaimVogDateOfBirth: "1980-01-02",
				openid4vpverifier.ClaimVogIssueDate:   "not-a-date",
			},
			wantErr: errDisclosureInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractVog(presentation(tt.claims), []string{"11", "22"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.GivenNames != tt.want.GivenNames || got.Surname != tt.want.Surname ||
				got.DateOfBirth != tt.want.DateOfBirth || !got.IssueDate.Equal(tt.want.IssueDate) ||
				!slices.Equal(got.AspectCodes, tt.want.AspectCodes) {
				t.Fatalf("vog = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// In a combined identity+VOG presentation each half is read from its own
// credential, so the identity's date of birth and the VOG's never overwrite
// each other - the whole point of matching one against the other.
func TestExtractIdentityAndVogReadTheirOwnCredential(t *testing.T) {
	res := openid4vpverifier.Presentation{
		Claims: map[string]string{
			openid4vpverifier.ClaimEmail:       "anna@example.test",
			openid4vpverifier.ClaimDateOfBirth: "1999-12-31", // whichever won the flattening
		},
		ByCredential: map[string]map[string]string{
			"passport": {
				openid4vpverifier.ClaimGivenNames:  "Anna",
				openid4vpverifier.ClaimFamilyName:  "Berg",
				openid4vpverifier.ClaimDateOfBirth: "1980-01-02",
			},
			"email": {openid4vpverifier.ClaimEmail: "anna@example.test"},
			"vog": {
				openid4vpverifier.ClaimVogGivenNames:   "Anna",
				openid4vpverifier.ClaimVogSurname:      "Berg",
				openid4vpverifier.ClaimVogDateOfBirth:  "1999-12-31",
				openid4vpverifier.ClaimVogIssueDate:    "2024-05-01",
				openid4vpverifier.VogAspectClaim("11"): "yes",
			},
		},
	}
	ident, err := extractIdentity(res)
	if err != nil {
		t.Fatalf("extractIdentity: %v", err)
	}
	if ident.DateOfBirth != "1980-01-02" || ident.Name.LastName != "Berg" {
		t.Errorf("identity = %+v, want the passport's date of birth", ident)
	}
	disclosedVog, err := extractVog(res, []string{"11"})
	if err != nil {
		t.Fatalf("extractVog: %v", err)
	}
	if disclosedVog.DateOfBirth != "1999-12-31" || !slices.Equal(disclosedVog.AspectCodes, []string{"11"}) {
		t.Errorf("vog = %+v, want the vog credential's date of birth and aspect 11", disclosedVog)
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
