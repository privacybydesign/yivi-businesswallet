package csc

import (
	"strings"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty is kept empty", in: "  ", want: ""},
		{name: "https is accepted", in: "https://qtsp.example.org", want: "https://qtsp.example.org"},
		{name: "http localhost sample", in: "http://localhost:8085", want: "http://localhost:8085"},
		{name: "host is lowercased", in: "https://QTSP.Example.ORG/csc", want: "https://qtsp.example.org/csc"},
		{name: "trailing slash dropped", in: "https://qtsp.example.org/", want: "https://qtsp.example.org"},
		{name: "fragment dropped", in: "https://qtsp.example.org/csc#frag", want: "https://qtsp.example.org/csc"},
		{name: "embedded credentials refused", in: "https://user:pw@qtsp.example.org", wantErr: true},
		{name: "non-http scheme refused", in: "ftp://qtsp.example.org", wantErr: true},
		{name: "no host refused", in: "https://", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeBaseURL(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeDefaultsAndValidatesProviderKind(t *testing.T) {
	// An empty kind defaults to the sample provider.
	out, err := Normalize(SettingsInput{BaseURL: "https://qtsp.example.org"})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.ProviderKind != ProviderKindSample {
		t.Errorf("default provider kind = %q, want %q", out.ProviderKind, ProviderKindSample)
	}

	// An unknown kind is refused.
	if _, err := Normalize(SettingsInput{ProviderKind: "nope"}); err == nil {
		t.Error("Normalize accepted an unknown provider kind, want error")
	}
}

func TestNormalizeTrimsSecretPointer(t *testing.T) {
	secret := "  s3cret  "
	out, err := Normalize(SettingsInput{ProviderKind: ProviderKindCustom, ClientSecret: &secret})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.ClientSecret == nil || *out.ClientSecret != "s3cret" {
		t.Errorf("ClientSecret = %v, want trimmed 's3cret'", out.ClientSecret)
	}
}

func TestKnownProviderKind(t *testing.T) {
	if !KnownProviderKind(ProviderKindSample) || !KnownProviderKind(ProviderKindCustom) {
		t.Error("the two shipped kinds must be known")
	}
	if KnownProviderKind("mystery") {
		t.Error("an unshipped kind must not be known")
	}
}

func TestProviderKindsExposeSampleDefaultURL(t *testing.T) {
	for _, k := range ProviderKinds() {
		if k.ID == ProviderKindSample && k.DefaultBaseURL != SampleBaseURL {
			t.Errorf("sample default base URL = %q, want %q", k.DefaultBaseURL, SampleBaseURL)
		}
	}
	if strings.TrimSpace(SampleBaseURL) == "" {
		t.Error("SampleBaseURL must not be empty")
	}
}
