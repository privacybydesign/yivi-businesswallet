package eudiholder

import (
	"reflect"
	"testing"

	"github.com/privacybydesign/irmago/eudi/storage/db/models"
	"gorm.io/datatypes"
)

func TestDecodeClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    map[string]any
	}{
		{
			name:    "empty payload yields empty map",
			payload: "",
			want:    map[string]any{},
		},
		{
			name:    "empty object yields empty map",
			payload: "{}",
			want:    map[string]any{},
		},
		{
			name:    "strips registered claims, keeps attributes",
			payload: `{"iss":"https://issuer","vct":"nl.kvk.registration","iat":1700000000,"cnf":{"jwk":{}},"_sd":["x"],"_sd_alg":"sha-256","fed":{"entity":"x"},"company_name":"Demo B.V.","kvk_number":"12345678"}`,
			want: map[string]any{
				"company_name": "Demo B.V.",
				"kvk_number":   "12345678",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeClaims([]byte(tc.payload))
			if err != nil {
				t.Fatalf("decodeClaims: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decodeClaims = %#v, want %#v", got, tc.want)
			}
		})
	}

	if _, err := decodeClaims([]byte("not json")); err == nil {
		t.Error("expected error for malformed payload")
	}
}

func TestAssembleAttributes(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"iss":"x","vct":"nl.kvk.registration","kvk_number":"123","company_name":"Demo B.V."}`)
	labels := map[string]string{"company_name": "Company name"}
	order := []string{"company_name"}

	got, err := assembleAttributes(payload, labels, order)
	if err != nil {
		t.Fatalf("assembleAttributes: %v", err)
	}

	// Metadata-ordered claim comes first with its label; the payload-only claim
	// follows (sorted) with an empty label; reserved claims are excluded.
	want := []HeldAttribute{
		{Key: "company_name", Label: "Company name", Value: "Demo B.V."},
		{Key: "kvk_number", Label: "", Value: "123"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d attributes, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("attribute %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestNestedClaimLabels(t *testing.T) {
	t.Parallel()

	claim := func(path, name string) models.CredentialClaim {
		return models.CredentialClaim{
			Path:    datatypes.JSON(path),
			Display: []models.ClaimDisplay{{Name: name, Locale: loc("en")}},
		}
	}
	meta := &models.CredentialMetadata{Claims: []models.CredentialClaim{
		claim(`["registered_address"]`, "Registered address"),
		claim(`["registered_address","post_code"]`, "Post code"),
		claim(`["legal_representative",null,"full_name"]`, "Full name"),
	}}

	got := nestedClaimLabels(meta, "en")
	want := map[string]map[string]string{
		"registered_address":   {"post_code": "Post code"},
		"legal_representative": {"[].full_name": "Full name"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nestedClaimLabels = %v, want %v", got, want)
	}
}
