package proofing

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const (
	reasonDocumentEN = "To check you are 18 or older."
	reasonDocumentNL = "Om te controleren dat je 18 of ouder bent."
)

func reasonSpec(attributes []string, reasons map[string]map[string]string) proofingprovider.FlowSpec {
	return proofingprovider.FlowSpec{
		Name: "Ticket", Steps: []string{"document_capture", "nfc_read"},
		RequestedAttributes: attributes, AttributeReasons: reasons,
	}
}

func documentReasons() map[string]map[string]string {
	return map[string]map[string]string{
		"dg1": {"en": reasonDocumentEN, "nl": reasonDocumentNL},
	}
}

func TestWithReasons(t *testing.T) {
	onlyEnglish := FlowHosted{Locales: []email.Locale{email.LocaleEN}}
	tests := []struct {
		name    string
		in      proofingprovider.FlowSpec
		offered FlowHosted
		wantErr string
	}{
		{"every language of a page that names none", reasonSpec([]string{"dg1"}, documentReasons()), DefaultFlowHosted(), ""},
		{
			"a missing language",
			reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": reasonDocumentEN}}),
			DefaultFlowHosted(), "give dg1 a reason in nl",
		},
		{
			"a blank reason is a missing one",
			reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": reasonDocumentEN, "nl": "  "}}),
			DefaultFlowHosted(), "give dg1 a reason in nl",
		},
		{
			"an item with no reasons at all",
			reasonSpec([]string{"dg1", "chip_checks"}, documentReasons()),
			DefaultFlowHosted(), "give chip_checks a reason in en",
		},
		{
			"only the languages the page offers",
			reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": reasonDocumentEN}}),
			onlyEnglish, "",
		},
		{"no data needs no reason", reasonSpec([]string{"outcome_only"}, nil), DefaultFlowHosted(), ""},
		{"a list that is left to the engine is not checked", reasonSpec(nil, nil), DefaultFlowHosted(), ""},
		{
			"a language that is not supported",
			reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": reasonDocumentEN, "nl": reasonDocumentNL, "de": "Zum Prüfen."}}),
			DefaultFlowHosted(), "unsupported language",
		},
		{
			"a reason past the bound",
			reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": strings.Repeat("a", maxAttributeReasonLength+1), "nl": reasonDocumentNL}}),
			DefaultFlowHosted(), "longer than",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := withReasons(tc.in, tc.offered)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("withReasons = %v, want accepted", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("withReasons = %v, want ErrInvalidInput containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestWithReasonsTrims(t *testing.T) {
	in := reasonSpec([]string{"dg1"}, map[string]map[string]string{
		"dg1": {"en": "  " + reasonDocumentEN + " ", "nl": reasonDocumentNL},
		"dg2": {"en": ""},
	})
	got, err := withReasons(in, DefaultFlowHosted())
	if err != nil {
		t.Fatalf("withReasons: %v", err)
	}
	if got.AttributeReasons["dg1"]["en"] != reasonDocumentEN {
		t.Errorf("dg1 en = %q, want it trimmed", got.AttributeReasons["dg1"]["en"])
	}
	if _, ok := got.AttributeReasons["dg2"]; ok {
		t.Error("an item whose reasons are all blank keeps no entry")
	}
	if in.AttributeReasons["dg1"]["en"] != "  "+reasonDocumentEN+" " {
		t.Error("the caller's map was changed")
	}
}

func TestCreateFlowNeedsReasons(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	missing := reasonSpec([]string{"dg1"}, map[string]map[string]string{"dg1": {"en": reasonDocumentEN}})
	if _, err := f.svc.CreateFlow(ctx, testOrg, missing); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateFlow without the Dutch reason = %v, want ErrInvalidInput", err)
	}
	if len(f.ips.createdFlows) != 0 {
		t.Fatal("a refused flow reached the engine")
	}

	if _, err := f.svc.CreateFlow(ctx, testOrg, reasonSpec([]string{"dg1"}, documentReasons())); err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	if got := f.ips.createdFlows[0].AttributeReasons["dg1"]["nl"]; got != reasonDocumentNL {
		t.Errorf("stored Dutch reason = %q, want %q", got, reasonDocumentNL)
	}
}

// A new version needs the reasons in the languages the flow's page offers when
// it is saved, not every supported one.
func TestEditFlowReasonLocales(t *testing.T) {
	f := newFixture()
	hosted := &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	f.svc.flowHostedSettings = hosted
	ctx := context.Background()
	english := map[string]map[string]string{"dg1": {"en": reasonDocumentEN}}

	if _, err := f.svc.EditFlow(ctx, testOrg, chipFlow.ID, reasonSpec([]string{"dg1"}, english)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("EditFlow on a page offering both languages = %v, want ErrInvalidInput", err)
	}

	hosted.byFlow[chipFlow.ID] = FlowHosted{Enabled: true, Locales: []email.Locale{email.LocaleEN}, Completion: CompletionRedirect}
	if _, err := f.svc.EditFlow(ctx, testOrg, chipFlow.ID, reasonSpec([]string{"dg1"}, maps.Clone(english))); err != nil {
		t.Fatalf("EditFlow on an English-only page: %v", err)
	}
}
