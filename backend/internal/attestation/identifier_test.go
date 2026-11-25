package attestation

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeIdentifierFollowsTheWEBUILDRule(t *testing.T) {
	got, err := normalizeIdentifier(map[string]any{"euid": " NLNHR.90000010 ", "lei": "", "tax": "NL123"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"euid": "NLNHR.90000010", "tax": "NL123"}; !reflect.DeepEqual(got, want) {
		t.Errorf("normalized = %v, want %v (trimmed, empty members dropped)", got, want)
	}
	if _, err := normalizeIdentifier(map[string]any{"lei": "5493001KJTIIGC8Y1R12"}); err != nil {
		t.Errorf("an LEI alone is enough: %v", err)
	}

	cases := []struct {
		name  string
		value any
		want  error
	}{
		{"absent", nil, ErrInvalidAttribute},
		{"not an object", "NLNHR.90000010", ErrInvalidAttribute},
		{"all members empty", map[string]any{"euid": " ", "lei": ""}, ErrMissingAttribute},
		{"unknown member", map[string]any{"euid": "NLNHR.1", "duns": "123"}, ErrInvalidAttribute},
		{"member not text", map[string]any{"euid": 42}, ErrInvalidAttribute},
		{"malformed LEI", map[string]any{"lei": "TOO-SHORT"}, ErrInvalidAttribute},
	}
	for _, tc := range cases {
		if _, err := normalizeIdentifier(tc.value); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestValidateAttributesRequiresTheIdentifierOnOrganizationSchemas(t *testing.T) {
	detail := TemplateDetail{SubjectType: SubjectOrganization, SchemaAttributes: []AttributeDef{{Key: "name", Required: true}}}
	if err := validateAttributes(detail, map[string]any{"name": "Zonnedael B.V."}); !errors.Is(err, ErrMissingAttribute) {
		t.Errorf("without identifier: err = %v, want ErrMissingAttribute", err)
	}
	if err := validateAttributes(detail, map[string]any{"name": "Zonnedael B.V.", IdentifierClaim: map[string]any{"euid": "NLNHR.1"}}); err != nil {
		t.Errorf("with identifier: %v", err)
	}
	person := TemplateDetail{SubjectType: SubjectNaturalPerson, SchemaAttributes: []AttributeDef{{Key: "name"}}}
	if err := validateAttributes(person, map[string]any{IdentifierClaim: map[string]any{"euid": "NLNHR.1"}}); !errors.Is(err, ErrUnknownAttribute) {
		t.Errorf("a person schema has no identifier: err = %v", err)
	}
}

func TestBuildIssuerConfigListsTheIdentifierForOrganizationSchemas(t *testing.T) {
	s := sampleSchema()
	s.SubjectType = SubjectOrganization
	var paths [][]any
	for _, c := range BuildIssuerConfig(s, "", "").VCT.Document.Claims {
		paths = append(paths, c.Path)
	}
	want := [][]any{{"email"}, {"role"}, {"identifier"}, {"identifier", "euid"}, {"identifier", "lei"}, {"identifier", "tax"}, {"identifier", "other"}}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("claim paths = %v, want %v", paths, want)
	}
}
