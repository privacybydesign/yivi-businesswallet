package attestation

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func rulebookDetail(t *testing.T, id string) (TemplateDetail, *rulebook.Entry) {
	t.Helper()
	sc, err := SchemaFromRulebook(id, "")
	if err != nil {
		t.Fatalf("SchemaFromRulebook(%q): %v", id, err)
	}
	rb, _ := rulebook.Get(id)
	return TemplateDetail{SchemaVCT: sc.VCT, SubjectType: sc.SubjectType, SchemaAttributes: sc.Attributes, SchemaRulebook: id}, rb
}

func TestSchemaFromRulebook(t *testing.T) {
	sc, err := SchemaFromRulebook("eucc", "")
	if err != nil {
		t.Fatal(err)
	}
	if sc.VCT != "uri:eu.eudi.eucc.1" || sc.Rulebook != "eucc" || sc.SubjectType != SubjectOrganization {
		t.Errorf("schema = %+v", sc)
	}
	if sc.CredentialConfigID != "we-build-eucc" {
		t.Errorf("default credentialConfigId = %q", sc.CredentialConfigID)
	}
	keys := make([]string, 0, len(sc.Attributes))
	for _, a := range sc.Attributes {
		keys = append(keys, a.Key)
	}
	if !slices.Contains(keys, "registered_address") || slices.Contains(keys, "iss") {
		t.Errorf("attribute keys = %v", keys)
	}

	custom, err := SchemaFromRulebook("lei", "my-lei")
	if err != nil || custom.CredentialConfigID != "my-lei" {
		t.Errorf("custom credentialConfigId = %q, %v", custom.CredentialConfigID, err)
	}
	if _, err := SchemaFromRulebook("nope", ""); !errors.Is(err, ErrUnknownRulebook) {
		t.Errorf("unknown rulebook err = %v", err)
	}
}

func TestKeepRulebookFields(t *testing.T) {
	existing, _ := SchemaFromRulebook("lei", "")
	in := Schema{DisplayName: "Renamed", SubjectType: SubjectNaturalPerson, Attributes: []AttributeDef{{Key: "x"}}}
	got := keepRulebookFields(existing, in)
	if got.DisplayName != "Renamed" || got.SubjectType != existing.SubjectType || len(got.Attributes) != len(existing.Attributes) {
		t.Errorf("rulebook schema update = %+v", got)
	}
	own := keepRulebookFields(Schema{}, in)
	if own.SubjectType != SubjectNaturalPerson || own.Attributes[0].Key != "x" {
		t.Errorf("organization schema update changed: %+v", own)
	}
}

func TestValidateAttributesRulebookSchema(t *testing.T) {
	detail, rb := rulebookDetail(t, "eucc")
	if err := validateAttributes(detail, rb.Sample()); err != nil {
		t.Fatalf("sample claims: %v", err)
	}
	claims := rb.Sample()
	delete(claims, "legal_person_name")
	if err := validateAttributes(detail, claims); !errors.Is(err, rulebook.ErrInvalidClaims) {
		t.Errorf("missing claim err = %v, want ErrInvalidClaims", err)
	}
	if err := validateAttributes(TemplateDetail{SchemaRulebook: "gone"}, map[string]any{}); !errors.Is(err, ErrUnknownRulebook) {
		t.Errorf("unknown rulebook err = %v", err)
	}
}

func TestValidateAttributesOrganizationSchema(t *testing.T) {
	detail := TemplateDetail{SchemaAttributes: []AttributeDef{
		{Key: "name", Type: AttributeTypeString, Required: true},
		{Key: "dept", Type: AttributeTypeString},
	}}
	cases := []struct {
		name  string
		attrs map[string]any
		want  error
	}{
		{"valid", map[string]any{"name": "Ada", "dept": "R&D"}, nil},
		{"unknown key", map[string]any{"name": "Ada", "age": "3"}, ErrUnknownAttribute},
		{"missing required", map[string]any{"dept": "R&D"}, ErrMissingAttribute},
		{"empty required", map[string]any{"name": ""}, ErrMissingAttribute},
		{"non-string value", map[string]any{"name": map[string]any{"first": "Ada"}}, ErrInvalidAttribute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAttributes(detail, tc.attrs)
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBuildIssuerConfigRulebookClaims(t *testing.T) {
	sc, _ := SchemaFromRulebook("eucc", "")
	cfg := BuildIssuerConfig(sc, "https://issuer.example", "")
	raw, err := json.Marshal(cfg.Metadata[sc.CredentialConfigID].CredentialMetadata.Claims)
	if err != nil {
		t.Fatal(err)
	}
	var claims []struct {
		Path []any `json:"path"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range claims {
		if len(c.Path) == 3 && c.Path[0] == "legal_representative" && c.Path[1] == nil && c.Path[2] == "full_name" {
			found = true
		}
	}
	if !found {
		t.Errorf("no [legal_representative, null, full_name] path in %s", raw)
	}
	for _, c := range cfg.VCT.Document.Claims {
		if c.SD != vctSelectiveDisclosure {
			t.Errorf("EUCC claim %v sd = %q", c.Path, c.SD)
		}
	}

	lei, _ := SchemaFromRulebook("lei", "")
	for _, c := range BuildIssuerConfig(lei, "", "").VCT.Document.Claims {
		if c.SD != vctNoSelectiveDisclosure {
			t.Errorf("LEI claim %v sd = %q, want %q", c.Path, c.SD, vctNoSelectiveDisclosure)
		}
	}
}

func TestCheckRulebook(t *testing.T) {
	rb, _ := rulebook.Get("lei")
	sample := rb.Sample()
	attrs := make([]eudiholder.HeldAttribute, 0, len(sample))
	for k, v := range sample {
		attrs = append(attrs, eudiholder.HeldAttribute{Key: k, Value: v})
	}

	id, violations, err := checkRulebook(rb.VCT, attrs, nil)
	if err != nil || id != "lei" || len(violations) != 0 {
		t.Errorf("valid LEI: id=%q violations=%v err=%v", id, violations, err)
	}

	broken := append(slices.Clone(attrs), eudiholder.HeldAttribute{Key: "lei_status", Value: "ACTIVE"})
	_, violations, err = checkRulebook(rb.VCT, broken, nil)
	if err != nil || len(violations) == 0 {
		t.Errorf("invalid LEI: violations=%v err=%v", violations, err)
	}

	id, violations, err = checkRulebook("nl.yivi.email", attrs, nil)
	if err != nil || id != "" || violations != nil {
		t.Errorf("non-rulebook vct: id=%q violations=%v err=%v", id, violations, err)
	}
}

// heldPayload is a held EUCC as a WE BUILD conformant issuer signs it.
func heldPayload() (*rulebook.Entry, map[string]any) {
	rb, _ := rulebook.Get("eucc")
	payload := rb.Sample()
	payload["vct"] = rb.VCT
	payload["iss"] = "did:web:issuer.example"
	payload["iat"] = 1736935200
	payload["exp"] = 1768471200
	payload["cnf"] = map[string]any{"jwk": map[string]any{"kty": "EC", "crv": "P-256", "x": "AQ", "y": "AQ"}}
	payload["status"] = map[string]any{"status_list": map[string]any{"idx": 7, "uri": "https://issuer.example/sl/1"}}
	return rb, payload
}

func TestCheckRulebookChecksTheWholeSignedPayload(t *testing.T) {
	rb, payload := heldPayload()
	if _, violations, err := checkRulebook(rb.VCT, nil, payload); err != nil || len(violations) != 0 {
		t.Fatalf("conformant EUCC: violations=%v err=%v", violations, err)
	}

	// What an issuer that ignores the rulebook signs: claims the WE BUILD schema
	// does not allow and a key binding it does not accept.
	_, broken := heldPayload()
	broken["fed"] = "https://issuer.example/org"
	broken["cnf"] = map[string]any{"kid": "did:key:zDnae#0"}
	_, violations, err := checkRulebook(rb.VCT, nil, broken)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(violations, " ")
	for _, want := range []string{"fed", "jwk"} {
		if !strings.Contains(joined, want) {
			t.Errorf("violations %v do not name %q", violations, want)
		}
	}
}

func TestDecodeSchemaForRulebookSchemaAcceptsItsOwnAttributes(t *testing.T) {
	existing, _ := SchemaFromRulebook("eucc", "")
	body, err := json.Marshal(map[string]any{
		"displayName": "Company certificate", "credentialConfigId": existing.CredentialConfigID,
		"attributes": existing.Attributes,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PATCH", "/orgs/acme/attestations/schemas/x", bytes.NewReader(body))
	if _, err := decodeSchemaFor(r, existing); err != nil {
		t.Fatalf("rulebook schema update: %v", err)
	}

	r = httptest.NewRequest("PATCH", "/orgs/acme/attestations/schemas/x", bytes.NewReader(body))
	if _, err := decodeSchemaFor(r, Schema{}); err == nil {
		t.Error("organization schema accepted an object attribute type")
	}
}
