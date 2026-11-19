package safehttp

import (
	"errors"
	"net"
	"testing"
)

func TestIsPublic(t *testing.T) {
	private := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1",
		"0.1.2.3", "198.18.0.1", "198.19.255.254", "240.0.0.1", "255.255.255.255",
		"64:ff9b::a00:1", "64:ff9b:1::1", "2002:a00:1::1", "::ffff:10.0.0.1", "::ffff:198.18.0.1",
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", "2001::1", "::10.0.0.1", "::93.184.216.34",
	}
	for _, ip := range private {
		if IsPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified public", ip)
		}
	}
	for _, ip := range []string{
		"93.184.216.34", "198.20.0.1", "100.128.0.1", "2606:2800:220:1:248:1893:25c8:1946",
		"::ffff:93.184.216.34", "2001:db9::1", "2001:4860:4860::8888",
	} {
		if !IsPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified non-public", ip)
		}
	}
}

func TestPolicyCheckURL(t *testing.T) {
	strict, insecure := Policy{}, Policy{AllowInsecureHTTP: true}
	cases := []struct {
		name    string
		p       Policy
		url     string
		wantErr error
	}{
		{"https ok", strict, "https://verifier.example.com/r", nil},
		{"http refused", strict, "http://verifier.example.com/r", ErrNotHTTPS},
		{"http allowed in dev", insecure, "http://localhost:8080/r", nil},
		{"relative", strict, "/r", ErrNotAbsolute},
		{"userinfo", strict, "https://user@verifier.example.com/r", ErrUserInfo},
		{"other scheme", insecure, "ftp://verifier.example.com/r", ErrNotHTTPS},
		{"javascript", insecure, "javascript:alert(1)", ErrNotAbsolute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.CheckURL(tc.url)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("CheckURL(%q) = %v, want nil", tc.url, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckURL(%q) = %v, want %v", tc.url, err, tc.wantErr)
			}
		})
	}
}
