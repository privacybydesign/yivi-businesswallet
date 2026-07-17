package attestation

import (
	"encoding/json"
	"testing"
)

// sampleSchema mirrors an org schema with localized credential + attribute
// display, the way the schema editor stores it.
func sampleSchema() Schema {
	return Schema{
		VCT:                "nl.caesar.employee",
		DisplayName:        "Employee of Caesar Groep",
		CredentialConfigID: "nl.caesar.employee",
		Attributes: []AttributeDef{
			{
				Key:      "email",
				Label:    "Email",
				Type:     AttributeTypeString,
				Required: true,
				Display: []LocalizedLabel{
					{Lang: "en", Label: "Email"},
					{Lang: "nl", Label: "E-mailadres"},
				},
			},
			{
				Key:      "role",
				Label:    "Role",
				Type:     AttributeTypeString,
				Required: false,
			},
		},
		Display: []LocalizedName{
			{Lang: "en", Name: "Employee"},
			{Lang: "nl", Name: "Werknemer"},
		},
	}
}

func TestBuildIssuerConfigMapsCredentialAndClaimDisplay(t *testing.T) {
	cfg := BuildIssuerConfig(sampleSchema(), "https://issuer.example/test-issuer")

	if cfg.CredentialConfigID != "nl.caesar.employee" {
		t.Fatalf("credentialConfigId: got %q", cfg.CredentialConfigID)
	}

	entry, ok := cfg.Metadata["nl.caesar.employee"]
	if !ok {
		t.Fatalf("metadata not keyed by credential config id: %+v", cfg.Metadata)
	}
	if entry.Format != credentialFormatSDJWT {
		t.Fatalf("format: got %q want %q", entry.Format, credentialFormatSDJWT)
	}
	if entry.Scope != "nl.caesar.employee" {
		t.Fatalf("scope: got %q", entry.Scope)
	}

	// Credential-level display: {lang,name} -> {name,locale}.
	if len(entry.CredentialMetadata.Display) != 2 ||
		entry.CredentialMetadata.Display[0] != (localeDisplay{Name: "Employee", Locale: "en"}) ||
		entry.CredentialMetadata.Display[1] != (localeDisplay{Name: "Werknemer", Locale: "nl"}) {
		t.Fatalf("credential display not mapped: %+v", entry.CredentialMetadata.Display)
	}
	// Top-level display mirrors credential_metadata.display (matches the file style).
	if len(entry.Display) != 2 || entry.Display[0].Name != "Employee" {
		t.Fatalf("top-level display not mapped: %+v", entry.Display)
	}

	// Claims: path is [key], attribute label -> {name,locale}, required -> mandatory.
	claims := entry.CredentialMetadata.Claims
	if len(claims) != 2 {
		t.Fatalf("expected 2 claims, got %d", len(claims))
	}
	if len(claims[0].Path) != 1 || claims[0].Path[0] != "email" {
		t.Fatalf("claim path not [key]: %+v", claims[0].Path)
	}
	if !claims[0].Mandatory {
		t.Fatalf("required attribute should map to mandatory: %+v", claims[0])
	}
	if len(claims[0].Display) != 2 ||
		claims[0].Display[1] != (localeDisplay{Name: "E-mailadres", Locale: "nl"}) {
		t.Fatalf("claim display not mapped: %+v", claims[0].Display)
	}
	// An attribute without translations still appears as a claim, without display.
	if claims[1].Path[0] != "role" || claims[1].Mandatory || len(claims[1].Display) != 0 {
		t.Fatalf("untranslated optional claim wrong: %+v", claims[1])
	}

	// credential_definition.claims carries the same claim list (what the SD-JWT
	// converter actually reads to build credential_metadata).
	if len(entry.CredentialDefinition.Claims) != 2 {
		t.Fatalf("credential_definition.claims: got %d", len(entry.CredentialDefinition.Claims))
	}
}

func TestBuildIssuerConfigVCTDocument(t *testing.T) {
	cfg := BuildIssuerConfig(sampleSchema(), "https://issuer.example/test-issuer")

	if cfg.VCT.Path != "/vct/nl-caesar-employee" {
		t.Fatalf("vct path: got %q", cfg.VCT.Path)
	}
	if len(cfg.VCT.Credentials) != 1 || cfg.VCT.Credentials[0] != "nl.caesar.employee" {
		t.Fatalf("vct credentials: %+v", cfg.VCT.Credentials)
	}
	if cfg.VCT.Document.Issuer != "https://issuer.example/test-issuer" {
		t.Fatalf("vct issuer: got %q", cfg.VCT.Document.Issuer)
	}
	if cfg.VCT.Document.Name != "Employee of Caesar Groep" {
		t.Fatalf("vct name: got %q", cfg.VCT.Document.Name)
	}
	if len(cfg.VCT.Document.Claims) != 2 || cfg.VCT.Document.Claims[0].SD != vctSelectiveDisclosure {
		t.Fatalf("vct claims: %+v", cfg.VCT.Document.Claims)
	}
	// VCT display keeps the {lang,name} spelling used by conf/vct/*.json.
	if len(cfg.VCT.Document.Display) != 2 || cfg.VCT.Document.Display[0].Lang != "en" {
		t.Fatalf("vct display: %+v", cfg.VCT.Document.Display)
	}
}

func TestBuildIssuerBundle(t *testing.T) {
	schemas := []Schema{
		sampleSchema(),
		{CredentialConfigID: "", DisplayName: "skip me"}, // no config id -> skipped
		{
			CredentialConfigID: "nl.yivi.supplier",
			DisplayName:        "Approved supplier",
			Attributes:         []AttributeDef{{Key: "name", Required: true}},
			Display:            []LocalizedName{{Lang: "en", Name: "Approved supplier"}},
		},
	}
	bundle := BuildIssuerBundle("yivi", "Yivi B.V.", "data:image/png;base64,AAAA", schemas)

	if bundle.Instance != "yivi" {
		t.Fatalf("instance: got %q", bundle.Instance)
	}
	// Issuer registration + did:web use the ops repo's substitution placeholders.
	if bundle.Issuer.Name != "yivi" || bundle.Issuer.BaseURL != "VERAMO_ISSUER_BASEURL/yivi" {
		t.Fatalf("issuer file: %+v", bundle.Issuer)
	}
	if bundle.Issuer.AdminToken != "VERAMO_ISSUER_ADMIN_TOKEN" || bundle.Issuer.DID != "yivi-did" {
		t.Fatalf("issuer token/did: %+v", bundle.Issuer)
	}
	if bundle.DID.DID != "did:web:VERAMO_ISSUER_DID_HOST:yivi:.well-known" || bundle.DID.Alias != "yivi-did" {
		t.Fatalf("did file: %+v", bundle.DID)
	}
	if bundle.DID.Services == nil {
		t.Fatalf("did services should be [] not null")
	}

	// Metadata carries a config per schema with a non-empty credential config id.
	if len(bundle.Metadata.CredentialConfigurationsSupported) != 2 {
		t.Fatalf("expected 2 credential configs, got %d", len(bundle.Metadata.CredentialConfigurationsSupported))
	}
	if _, ok := bundle.Metadata.CredentialConfigurationsSupported["nl.caesar.employee"]; !ok {
		t.Fatalf("employee config missing: %+v", bundle.Metadata.CredentialConfigurationsSupported)
	}
	if bundle.Metadata.Issuer != "VERAMO_ISSUER_BASEURL/yivi" {
		t.Fatalf("metadata issuer: %q", bundle.Metadata.Issuer)
	}
	if bundle.Metadata.AuthorizationServers == nil {
		t.Fatalf("authorization_servers should be [] not null")
	}
	// Branding: en + nl with the logo.
	if len(bundle.Metadata.Display) != 2 || bundle.Metadata.Display[0].Name != "Yivi B.V." ||
		bundle.Metadata.Display[0].Logo == nil {
		t.Fatalf("issuer display branding: %+v", bundle.Metadata.Display)
	}

	// One VCT doc per non-empty schema, keyed by slug.
	if len(bundle.VCTs) != 2 {
		t.Fatalf("expected 2 vct docs, got %d", len(bundle.VCTs))
	}
	if bundle.VCTs[0].Name != "nl-caesar-employee" {
		t.Fatalf("vct slug: %q", bundle.VCTs[0].Name)
	}
}

// TestBuildIssuerConfigJSONShape locks the emitted JSON keys to the exact names
// the Veramo issuer's conf/metadata/<instance>.json expects.
func TestBuildIssuerConfigJSONShape(t *testing.T) {
	cfg := BuildIssuerConfig(sampleSchema(), "https://issuer.example/test-issuer")
	raw, err := json.Marshal(cfg.Metadata["nl.caesar.employee"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"format", "scope", "cryptographic_binding_methods_supported",
		"credential_signing_alg_values_supported", "proof_types_supported",
		"credential_definition", "credential_metadata", "display",
	} {
		if _, ok := m[key]; !ok {
			t.Fatalf("missing top-level key %q in %s", key, raw)
		}
	}
	cm, ok := m["credential_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("credential_metadata not an object: %s", raw)
	}
	claims, ok := cm["claims"].([]any)
	if !ok || len(claims) == 0 {
		t.Fatalf("credential_metadata.claims missing: %s", raw)
	}
	claim0 := claims[0].(map[string]any)
	if _, ok := claim0["path"]; !ok {
		t.Fatalf("claim missing path: %s", raw)
	}
	disp := claim0["display"].([]any)
	d0 := disp[0].(map[string]any)
	if _, ok := d0["name"]; !ok {
		t.Fatalf("claim display uses wrong key (want name): %s", raw)
	}
	if _, ok := d0["locale"]; !ok {
		t.Fatalf("claim display uses wrong key (want locale): %s", raw)
	}
}
