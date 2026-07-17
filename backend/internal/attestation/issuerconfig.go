package attestation

import "strings"

// This file generates the Veramo issuer provisioning for a schema. In the target
// deployment the hosted issuer's runtime config API is disabled (no global admin
// BEARER_TOKEN); its credential display metadata is provisioned by GitOps files
// in openid4vc-poc-ops (conf/metadata/<instance>.json + conf/vct/*.json) and
// rolled out via a ConfigMap. So we do not push display at runtime — instead we
// map a schema's localized display metadata into the exact file shapes an
// operator commits to that repo. This is what makes a wallet render the
// credential and its claims in the holder's language; without it the schema's
// translations never reach the credential. See .ai/features/attestations.md.

const (
	// credentialFormatSDJWT is the SD-JWT VC format the issuer signs.
	credentialFormatSDJWT = "dc+sd-jwt"
	// sdjwtSigningAlg / bindingMethodDidJwk mirror the values every credential
	// entry in conf/metadata/<instance>.json declares (the issuer overrides them
	// anyway, but we emit them so the fragment is a faithful drop-in).
	sdjwtSigningAlg     = "ES256"
	bindingMethodDidJwk = "did:jwk"
	// defaultVctLanguage is the primary language a VCT document declares.
	defaultVctLanguage = "en"
	// vctSelectiveDisclosure marks a claim as selectively disclosable in a VCT
	// document (the "sd" field), matching the existing conf/vct/*.json files.
	vctSelectiveDisclosure = "allowed"

	// Placeholder tokens the openid4vc-poc-ops repo substitutes at deploy time
	// (see veramo-issuer.tf). Emitting them makes a generated issuer bundle a
	// drop-in for that repo's conf/ files.
	opsBaseURLPlaceholder    = "VERAMO_ISSUER_BASEURL"
	opsDIDHostPlaceholder    = "VERAMO_ISSUER_DID_HOST"
	opsAdminTokenPlaceholder = "VERAMO_ISSUER_ADMIN_TOKEN"

	didProviderWeb = "did:web"
	didKeyType     = "Secp256r1"
)

// localeDisplay is one language's rendering of a name, in the {name, locale}
// shape OpenID4VCI credential/claim `display` arrays use (note: our stored
// schema uses {lang, name|label}; this is the issuer-side spelling).
type localeDisplay struct {
	Name   string `json:"name"`
	Locale string `json:"locale"`
}

// claimConfig is one attribute in a credential configuration: its disclosure
// path plus localized labels.
type claimConfig struct {
	Path      []string        `json:"path"`
	Mandatory bool            `json:"mandatory,omitempty"`
	Display   []localeDisplay `json:"display,omitempty"`
}

type credentialMetadata struct {
	Display []localeDisplay `json:"display,omitempty"`
	Claims  []claimConfig   `json:"claims,omitempty"`
}

type credentialDefinition struct {
	Type   []string      `json:"type"`
	Claims []claimConfig `json:"claims,omitempty"`
}

// credentialConfiguration is one credential_configurations_supported[<id>] entry
// in the issuer metadata, in the exact shape conf/metadata/<instance>.json uses.
type credentialConfiguration struct {
	Format                               string               `json:"format"`
	Scope                                string               `json:"scope"`
	CryptographicBindingMethodsSupported []string             `json:"cryptographic_binding_methods_supported"`
	CredentialSigningAlgValuesSupported  []string             `json:"credential_signing_alg_values_supported"`
	ProofTypesSupported                  map[string]any       `json:"proof_types_supported"`
	CredentialDefinition                 credentialDefinition `json:"credential_definition"`
	CredentialMetadata                   credentialMetadata   `json:"credential_metadata"`
	Display                              []localeDisplay      `json:"display,omitempty"`
}

// vctDocument is a conf/vct/*.json entry: the SD-JWT VC Type Metadata for a
// credential type, keyed to the credential config id it applies to.
type vctDocument struct {
	Path        string          `json:"path"`
	Credentials []string        `json:"credentials"`
	Document    vctDocumentBody `json:"document"`
}

type vctDocumentBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Language    string          `json:"language"`
	Display     []LocalizedName `json:"display"`
	Claims      []vctClaim      `json:"claims"`
	Issuer      string          `json:"issuer"`
}

type vctClaim struct {
	Path []string `json:"path"`
	SD   string   `json:"sd"`
}

// IssuerConfig is the generated Veramo provisioning for a schema: the metadata
// fragment (keyed by credential config id, ready to merge into a metadata file's
// credential_configurations_supported) and the matching VCT document.
type IssuerConfig struct {
	CredentialConfigID string                             `json:"credentialConfigId"`
	Metadata           map[string]credentialConfiguration `json:"metadata"`
	VCT                vctDocument                        `json:"vct"`
}

// BuildIssuerConfig maps a schema's localized display metadata (credential name
// + per-claim labels, per language) to the Veramo issuer GitOps config. issuerURL
// is the issuer instance base URL written into the VCT document's `issuer` field
// (empty is fine — the operator fills it in for the target environment).
func BuildIssuerConfig(s Schema, issuerURL string) IssuerConfig {
	claims := make([]claimConfig, 0, len(s.Attributes))
	vctClaims := make([]vctClaim, 0, len(s.Attributes))
	for _, a := range s.Attributes {
		claims = append(claims, claimConfig{
			Path:      []string{a.Key},
			Mandatory: a.Required,
			Display:   labelDisplays(a.Display),
		})
		vctClaims = append(vctClaims, vctClaim{Path: []string{a.Key}, SD: vctSelectiveDisclosure})
	}

	credDisplay := nameDisplays(s.Display)
	cfg := credentialConfiguration{
		Format:                               credentialFormatSDJWT,
		Scope:                                s.CredentialConfigID,
		CryptographicBindingMethodsSupported: []string{bindingMethodDidJwk},
		CredentialSigningAlgValuesSupported:  []string{sdjwtSigningAlg},
		ProofTypesSupported: map[string]any{
			"jwt": map[string]any{"proof_signing_alg_values_supported": []string{sdjwtSigningAlg}},
		},
		CredentialDefinition: credentialDefinition{
			Type:   []string{"VerifiableCredential", s.CredentialConfigID},
			Claims: claims,
		},
		CredentialMetadata: credentialMetadata{Display: credDisplay, Claims: claims},
		Display:            credDisplay,
	}

	return IssuerConfig{
		CredentialConfigID: s.CredentialConfigID,
		Metadata:           map[string]credentialConfiguration{s.CredentialConfigID: cfg},
		VCT: vctDocument{
			Path:        "/vct/" + vctSlug(vctSlugSource(s)),
			Credentials: []string{s.CredentialConfigID},
			Document: vctDocumentBody{
				Name:        s.DisplayName,
				Description: "VCT metadata for " + s.DisplayName,
				Language:    defaultVctLanguage,
				Display:     s.Display,
				Claims:      vctClaims,
				Issuer:      issuerURL,
			},
		},
	}
}

// --- Per-organization issuer bundle (full GitOps drop-in) ---

// issuerInstanceFile is conf/issuer/<instance>.json — registers the org's issuer
// instance at the hosted issuer.
type issuerInstanceFile struct {
	Name       string `json:"name"`
	BaseURL    string `json:"baseUrl"`
	AdminToken string `json:"adminToken"`
	DID        string `json:"did"`
	UsesNonces bool   `json:"usesNonces"`
}

// didFile is conf/dids/<instance>-did.json — the did:web key for the instance.
type didFile struct {
	DID      string   `json:"did"`
	Alias    string   `json:"alias"`
	Provider string   `json:"provider"`
	Type     string   `json:"type"`
	Services []string `json:"services"`
}

// issuerLogo / issuerDisplay are the issuer-level branding shown by wallets.
type issuerLogo struct {
	URI     string `json:"uri"`
	AltText string `json:"alt_text,omitempty"`
}

type issuerDisplay struct {
	Name   string      `json:"name"`
	Locale string      `json:"locale"`
	Logo   *issuerLogo `json:"logo,omitempty"`
}

// issuerMetadataFile is conf/metadata/<instance>.json — the instance's issuer
// metadata, whose credential_configurations_supported carries every schema's
// localized display.
type issuerMetadataFile struct {
	Issuer                            string                             `json:"issuer"`
	CredentialIssuer                  string                             `json:"credential_issuer"`
	Display                           []issuerDisplay                    `json:"display,omitempty"`
	AuthorizationServers              []string                           `json:"authorization_servers"`
	CredentialConfigurationsSupported map[string]credentialConfiguration `json:"credential_configurations_supported"`
}

// namedVCT pairs a filename stem with a VCT document (conf/vct/<name>.json).
type namedVCT struct {
	Name     string      `json:"name"`
	Document vctDocument `json:"document"`
}

// IssuerBundle is the full GitOps drop-in for an organization's issuer instance:
// the issuer registration, its did:web key, the issuer metadata (with every
// schema's localized display), and one VCT document per schema. An operator
// commits these to openid4vc-poc-ops (conf/issuer, conf/dids, conf/metadata,
// conf/vct) and redeploys. Files emit the ops repo's substitution placeholders.
type IssuerBundle struct {
	Instance string             `json:"instance"`
	Issuer   issuerInstanceFile `json:"issuer"`
	DID      didFile            `json:"did"`
	Metadata issuerMetadataFile `json:"metadata"`
	VCTs     []namedVCT         `json:"vcts"`
}

// BuildIssuerBundle assembles the per-org issuer GitOps bundle from the org's
// instance name + branding and its schemas' localized display metadata.
func BuildIssuerBundle(instance, displayName, logoURI string, schemas []Schema) IssuerBundle {
	issuerBase := opsBaseURLPlaceholder + "/" + instance
	didAlias := instance + "-did"

	configs := make(map[string]credentialConfiguration, len(schemas))
	vcts := make([]namedVCT, 0, len(schemas))
	for _, s := range schemas {
		if s.CredentialConfigID == "" {
			continue
		}
		ic := BuildIssuerConfig(s, issuerBase)
		for k, v := range ic.Metadata {
			configs[k] = v
		}
		vcts = append(vcts, namedVCT{Name: vctSlug(vctSlugSource(s)), Document: ic.VCT})
	}

	var display []issuerDisplay
	if displayName != "" {
		var logo *issuerLogo
		if logoURI != "" {
			logo = &issuerLogo{URI: logoURI, AltText: displayName + " logo"}
		}
		// Emit en + nl so wallets in either language show the issuer branding.
		display = []issuerDisplay{
			{Name: displayName, Locale: "en", Logo: logo},
			{Name: displayName, Locale: "nl", Logo: logo},
		}
	}

	return IssuerBundle{
		Instance: instance,
		Issuer: issuerInstanceFile{
			Name:       instance,
			BaseURL:    issuerBase,
			AdminToken: opsAdminTokenPlaceholder,
			DID:        didAlias,
			UsesNonces: true,
		},
		DID: didFile{
			DID:      "did:web:" + opsDIDHostPlaceholder + ":" + instance + ":.well-known",
			Alias:    didAlias,
			Provider: didProviderWeb,
			Type:     didKeyType,
			Services: []string{},
		},
		Metadata: issuerMetadataFile{
			Issuer:                            issuerBase,
			CredentialIssuer:                  issuerBase,
			Display:                           display,
			AuthorizationServers:              []string{},
			CredentialConfigurationsSupported: configs,
		},
		VCTs: vcts,
	}
}

// nameDisplays / labelDisplays convert the stored {lang, name|label} entries to
// the issuer-side {name, locale} spelling.
func nameDisplays(in []LocalizedName) []localeDisplay {
	out := make([]localeDisplay, 0, len(in))
	for _, d := range in {
		out = append(out, localeDisplay{Name: d.Name, Locale: d.Lang})
	}
	return out
}

func labelDisplays(in []LocalizedLabel) []localeDisplay {
	out := make([]localeDisplay, 0, len(in))
	for _, d := range in {
		out = append(out, localeDisplay{Name: d.Label, Locale: d.Lang})
	}
	return out
}

// vctSlugSource picks the identifier a VCT document's path/filename is derived
// from: the schema VCT (already org-namespaced, e.g. "nl.yivi.email", so VCT
// paths don't collide across orgs), falling back to the credential config id.
func vctSlugSource(s Schema) string {
	if s.VCT != "" {
		return s.VCT
	}
	return s.CredentialConfigID
}

// vctSlug derives a URL-path-safe slug from an identifier for the VCT
// document path (e.g. "nl.yivi.email" -> "nl-yivi-email").
func vctSlug(configID string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(configID) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
