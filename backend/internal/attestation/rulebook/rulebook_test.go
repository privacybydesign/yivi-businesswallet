package rulebook

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// vendoredSHA256 pins the files copied from upstream at UpstreamCommit. A new
// upstream version is vendored by replacing the files and these hashes together.
var vendoredSHA256 = map[string]string{
	"schemas/ds004-eucc-sd-jwt.json":          "fd3b1c8099a20dd78b32a3e3a28acf0a4dfdd17110c999b9944da2b3f6c1b770",
	"schemas/ds007-eu-poa-sd-jwt.json":        "6e2e36728b99d118b5bd101e063a5584cfc955775583adff61acabaf1c51c31b",
	"schemas/ubo-sd-jwt.json":                 "02c16b47402afc96c14222a620ab1e64a37b558007bb4a841e530ab282e9e02b",
	"schemas/vat-id-sd-jwt.json":              "6d452e74515089fa0bbda00a66cc22f673b54ad17acc960e05d0686deab986e6",
	"samples/ds004-eucc-sd-jwt-sample.json":   "25edfcfdbd3785dcb04b25cb9ca1ea9989f16990e5fc071e14ddf6364fd5176d",
	"samples/ds007-eu-poa-sd-jwt-sample.json": "ec09996eb90078134fc7ea3a3fa95fc05dcdb9896a7b1e67ec9fb38c4993f66e",
	"samples/ubo-sd-jwt-sample.json":          "d4cd2eeb63e6ae2254abbf09d28b5dcc5aff00b25ab44b5d73e3f056bceecdd5",
	"samples/vat-id-sd-jwt-sample.json":       "5ad31297401b7dfa96cbdb8f7a575eb7a8216f5e2f5729a7642aa797b881fcbb",
}

func TestVendoredFilesUnchanged(t *testing.T) {
	for name, want := range vendoredSHA256 {
		raw, err := files.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s: sha256 %s, want %s (vendored files must match upstream %s)", name, got, want, UpstreamCommit)
		}
	}
}

func TestSampleClaimsValidate(t *testing.T) {
	for _, e := range All() {
		t.Run(e.ID, func(t *testing.T) {
			if err := e.ValidateClaims(e.Sample()); err != nil {
				t.Fatalf("sample claims: %v", err)
			}
		})
	}
}

func TestPayloadNeedsProtocolClaimsTheSchemaRequires(t *testing.T) {
	e, _ := Get("vat-id")
	err := e.ValidatePayload(e.Sample())
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("payload without iss/iat/exp: err = %v, want a ValidationError", err)
	}
	if !strings.Contains(strings.Join(ve.Violations, " "), "iss") {
		t.Errorf("violations %v do not name the missing iss", ve.Violations)
	}

	payload := e.Sample()
	payload["vct"] = e.VCT
	payload["iss"] = "https://issuer.example"
	payload["iat"] = 1736935200
	payload["exp"] = 1768471200
	if err := e.ValidatePayload(payload); err != nil {
		t.Errorf("payload with protocol claims: %v", err)
	}
}

func TestStatusClaimAcceptsTheIETFShape(t *testing.T) {
	e, _ := Get("eucc")
	claims := e.Sample()
	claims["status"] = map[string]any{"status_list": map[string]any{"idx": 7, "uri": "https://issuer.example/status/1"}}
	if err := e.ValidateClaims(claims); err != nil {
		t.Fatalf("IETF status claim: %v", err)
	}
}

func TestInvalidClaimsAreRejected(t *testing.T) {
	cases := []struct {
		rulebook string
		mutate   func(map[string]any)
		mention  string
	}{
		{"eucc", func(c map[string]any) { delete(c, "legal_person_name") }, "legal_person_name"},
		{"eucc", func(c map[string]any) { c["registration_member_state"] = "Netherlands" }, "registration_member_state"},
		{"eucc", func(c map[string]any) { c["registration_date"] = "20-05-2020" }, "registration_date"},
		{"lei", func(c map[string]any) { c["lei_status"] = "ACTIVE" }, "lei_status"},
		{"lei", func(c map[string]any) { c["lei"] = "TOO-SHORT" }, "lei"},
		{"vat-id", func(c map[string]any) {
			unit, _ := c["administrative_unit"].(map[string]any)
			unit["economic_operator"] = map[string]any{
				"legal_person":   map[string]any{"legal_identifier": "NL1", "legal_name": "A"},
				"natural_person": map[string]any{"family_name": "B", "given_name": "C", "birth_date": "1980-01-01"},
			}
		}, "economic_operator"},
		{"ubo", func(c map[string]any) { c["ubo"] = []any{} }, "ubo"},
		{"eu-poa", func(c map[string]any) { c["unexpected_claim"] = "x" }, "unexpected_claim"},
	}
	for _, tc := range cases {
		t.Run(tc.rulebook+"/"+tc.mention, func(t *testing.T) {
			e, ok := Get(tc.rulebook)
			if !ok {
				t.Fatalf("no rulebook %q", tc.rulebook)
			}
			claims := e.Sample()
			tc.mutate(claims)
			err := e.ValidateClaims(claims)
			if !errors.Is(err, ErrInvalidClaims) {
				t.Fatalf("err = %v, want ErrInvalidClaims", err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}

func TestAttributesSkipProtocolClaims(t *testing.T) {
	for _, e := range All() {
		for _, a := range e.Attributes() {
			if _, protocol := protocolClaims[a.Key]; protocol {
				t.Errorf("%s: protocol claim %q listed as an attribute", e.ID, a.Key)
			}
		}
	}
	e, _ := Get("eucc")
	attrs := e.Attributes()
	if attrs[0].Key != "issuing_authority" {
		t.Errorf("first EUCC attribute = %q, want schema order (issuing_authority)", attrs[0].Key)
	}
	types := map[string]string{}
	required := map[string]bool{}
	for _, a := range attrs {
		types[a.Key] = a.Type
		required[a.Key] = a.Required
	}
	if types["registered_address"] != TypeObject || types["legal_representative"] != TypeArray || types["registration_date"] != TypeDate {
		t.Errorf("EUCC types = %v", types)
	}
	if !required["legal_person_name"] || required["share_capital"] {
		t.Errorf("EUCC required = %v", required)
	}
}

func TestClaimsWalkNestedPaths(t *testing.T) {
	e, _ := Get("eucc")
	want := map[string]bool{
		pathKey([]any{"registered_address", "post_code"}):        false,
		pathKey([]any{"legal_representative", nil, "full_name"}): false,
		pathKey([]any{"legal_representative"}):                   false,
	}
	for _, c := range e.Claims() {
		if _, ok := want[pathKey(c.Path)]; ok {
			want[pathKey(c.Path)] = true
		}
		if _, protocol := protocolClaims[c.Path[0].(string)]; protocol {
			t.Errorf("protocol claim path %v", c.Path)
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("claim path %s missing", p)
		}
	}
}

func TestForVCT(t *testing.T) {
	for _, e := range All() {
		got, ok := ForVCT(e.VCT)
		if !ok || got.ID != e.ID {
			t.Errorf("ForVCT(%q) = %v, %v", e.VCT, got, ok)
		}
	}
	if _, ok := ForVCT("nl.yivi.email"); ok {
		t.Error("ForVCT matched an org-defined vct")
	}
}

func pathKey(p []any) string {
	parts := make([]string, len(p))
	for i, s := range p {
		if s == nil {
			parts[i] = "[]"
			continue
		}
		parts[i] = s.(string)
	}
	return strings.Join(parts, ".")
}
