package attestation

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// decodeSchemaBody runs the shared schema decoder over a raw JSON body, the same
// path the create/update handlers take.
func decodeSchemaBody(t *testing.T, body string) (schemaRequest, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/orgs/acme/attestations/schemas", strings.NewReader(body))
	return decodeSchema(r)
}

func apiErrorCode(t *testing.T, err error) string {
	t.Helper()
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *respond.APIError, got %v", err)
	}
	return apiErr.Code
}

func TestDecodeSchemaRejectsUnsupportedAttributeType(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"attributes":[{"key":"age","label":"Age","type":"colour"}]}`
	_, err := decodeSchemaBody(t, body)
	if err == nil {
		t.Fatal("expected an error for an unsupported attribute type")
	}
	if code := apiErrorCode(t, err); code != "invalid_input" {
		t.Fatalf("expected invalid_input, got %q", code)
	}
}

func TestDecodeSchemaAcceptsSupportedAttributeTypes(t *testing.T) {
	for _, typ := range SupportedAttributeTypes {
		body := `{"displayName":"Employee","credentialConfigId":"cfg",
			"attributes":[{"key":"field","label":"Field","type":"` + typ + `"}]}`
		req, err := decodeSchemaBody(t, body)
		if err != nil {
			t.Fatalf("type %q: unexpected error: %v", typ, err)
		}
		if req.Attributes[0].Type != typ {
			t.Fatalf("type %q: not preserved, got %q", typ, req.Attributes[0].Type)
		}
	}
}

func TestValidateAttributeSources(t *testing.T) {
	personAttrs := []AttributeDef{
		{Key: "name", Type: AttributeTypeString},
		{Key: "dept", Type: AttributeTypeString},
	}
	orgAttrs := []AttributeDef{{Key: "kvk", Type: AttributeTypeString}}

	t.Run("valid person binding", func(t *testing.T) {
		err := ValidateAttributeSources(SubjectNaturalPerson, personAttrs, map[string]string{
			"name": SourceMemberFullName,
			"dept": SourceMemberDepartment,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("valid org binding", func(t *testing.T) {
		err := ValidateAttributeSources(SubjectOrganization, orgAttrs, map[string]string{
			"kvk": SourceOrgKVKNumber,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown attribute key rejected", func(t *testing.T) {
		err := ValidateAttributeSources(SubjectNaturalPerson, personAttrs, map[string]string{
			"missing": SourceMemberEmail,
		})
		if !errors.Is(err, ErrUnknownAttribute) {
			t.Fatalf("err = %v, want ErrUnknownAttribute", err)
		}
	})

	t.Run("wrong-subject token rejected", func(t *testing.T) {
		// An org token on a natural-person schema is not a valid source.
		err := ValidateAttributeSources(SubjectNaturalPerson, personAttrs, map[string]string{
			"name": SourceOrgKVKNumber,
		})
		if !errors.Is(err, ErrUnknownAttributeSource) {
			t.Fatalf("err = %v, want ErrUnknownAttributeSource", err)
		}
	})
}

func TestDecodeSchemaDefaultsEmptyAttributeTypeToString(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"attributes":[{"key":"name","label":"Name","type":""}]}`
	req, err := decodeSchemaBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Attributes[0].Type != AttributeTypeString {
		t.Fatalf("expected default %q, got %q", AttributeTypeString, req.Attributes[0].Type)
	}
}

func TestDecodeSchemaKeepsLocalizedCredentialNames(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"display":[{"lang":"en","name":"Employee"},{"lang":"nl","name":"Werknemer"}],
		"attributes":[{"key":"name","label":"Name","type":"string",
			"display":[{"lang":"nl","label":"Naam"}]}]}`
	req, err := decodeSchemaBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Display) != 2 {
		t.Fatalf("expected 2 credential display entries, got %d", len(req.Display))
	}
	if len(req.Attributes[0].Display) != 1 || req.Attributes[0].Display[0].Label != "Naam" {
		t.Fatalf("expected attribute display [nl:Naam], got %+v", req.Attributes[0].Display)
	}
}

func TestDecodeSchemaDropsEmptyLocalizedRows(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"display":[{"lang":"","name":""},{"lang":"en","name":"Employee"}],
		"attributes":[{"key":"name","label":"Name","type":"string"}]}`
	req, err := decodeSchemaBody(t, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Display) != 1 {
		t.Fatalf("expected the empty display row to be dropped, got %d", len(req.Display))
	}
}

func TestDecodeSchemaRejectsPartialLocalizedEntry(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"display":[{"lang":"en","name":""}],
		"attributes":[{"key":"name","label":"Name","type":"string"}]}`
	if _, err := decodeSchemaBody(t, body); err == nil {
		t.Fatal("expected an error for a language without text")
	}
}

func TestDecodeSchemaRejectsDuplicateLocalizedLanguage(t *testing.T) {
	body := `{"displayName":"Employee","credentialConfigId":"cfg",
		"display":[{"lang":"en","name":"Employee"},{"lang":"en","name":"Worker"}],
		"attributes":[{"key":"name","label":"Name","type":"string"}]}`
	if _, err := decodeSchemaBody(t, body); err == nil {
		t.Fatal("expected an error for a duplicate translation language")
	}
}
