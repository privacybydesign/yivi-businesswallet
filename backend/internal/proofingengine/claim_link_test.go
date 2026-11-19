package proofingengine

import (
	"net/url"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// A claim link is the app's vcmrtd://verify scheme by default, or the
// configured https App Link, with the same handover= and api= either way; the
// QR carries the same link as the button.
func TestGrantResponseLink(t *testing.T) {
	const api = "https://wallet.example.org"
	for name, tc := range map[string]struct {
		appLinkURL string
		scheme     string
		host       string
		path       string
	}{
		"vcmrtd scheme":  {"", "vcmrtd", "verify", ""},
		"https App Link": {"https://idem.yivi.app/verify", "https", "idem.yivi.app", "/verify"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &Server{cfg: Config{PublicBaseURL: api + "/", AppLinkURL: tc.appLinkURL}}
			resp := s.grantResponse(session.DeviceRoleNative, "tok&en", time.Now())
			if resp.QR != resp.DeepLink {
				t.Errorf("QR %q differs from the deep link %q", resp.QR, resp.DeepLink)
			}
			link, err := url.Parse(resp.DeepLink)
			if err != nil {
				t.Fatalf("deep link %q: %v", resp.DeepLink, err)
			}
			if link.Scheme != tc.scheme || link.Host != tc.host || link.Path != tc.path {
				t.Errorf("deep link %q: want %s://%s%s", resp.DeepLink, tc.scheme, tc.host, tc.path)
			}
			if got := link.Query().Get("handover"); got != "tok&en" {
				t.Errorf("handover = %q, want the token", got)
			}
			if got := link.Query().Get("api"); got != api {
				t.Errorf("api = %q, want %q", got, api)
			}
		})
	}
}
