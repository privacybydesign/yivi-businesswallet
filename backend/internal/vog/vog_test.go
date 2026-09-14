package vog

import "testing"

func TestIsFunctionAspect(t *testing.T) {
	for _, code := range FunctionAspects {
		if !IsFunctionAspect(code) {
			t.Errorf("IsFunctionAspect(%q) = false, want true", code)
		}
	}
	for _, code := range []string{"01", "99", "85x", ""} {
		if IsFunctionAspect(code) {
			t.Errorf("IsFunctionAspect(%q) = true, want false", code)
		}
	}
}

func TestDocumentCodes(t *testing.T) {
	d := Document{AspectCodes: []string{"11", "12"}, ProfileCodes: []string{"77"}}
	got := d.Codes()
	want := []string{"11", "12", "77"}
	if !equalStrings(got, want) {
		t.Errorf("Codes() = %v, want %v", got, want)
	}
}

func TestResponseCodeClassification(t *testing.T) {
	cases := []struct {
		code                        ResponseCode
		authentic, final, retryable bool
	}{
		{0, true, false, false},
		{1, false, true, false},
		{2, false, true, false},
		{6, false, true, false},
		{3, false, false, true},
		{4, false, false, true},
		{5, false, false, true},
		{7, false, false, true},
	}
	for _, c := range cases {
		if got := c.code.Authentic(); got != c.authentic {
			t.Errorf("ResponseCode(%d).Authentic() = %v, want %v", c.code, got, c.authentic)
		}
		if got := c.code.Final(); got != c.final {
			t.Errorf("ResponseCode(%d).Final() = %v, want %v", c.code, got, c.final)
		}
		if got := c.code.Retryable(); got != c.retryable {
			t.Errorf("ResponseCode(%d).Retryable() = %v, want %v", c.code, got, c.retryable)
		}
	}
}
