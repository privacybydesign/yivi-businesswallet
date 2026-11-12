package walletunit

import "testing"

func TestNewProviderTrimsBaseURL(t *testing.T) {
	p := NewProvider(nil, nil, nil, Config{BaseURL: "https://wallet.example/"})
	want := "https://wallet.example" + statusListPath + "0"
	if got := statusListURI(p.cfg.BaseURL, 0); got != want {
		t.Errorf("status list URI = %q, want %q", got, want)
	}
}
