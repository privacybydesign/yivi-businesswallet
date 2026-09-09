package openid4vppresenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/privacybydesign/irmago/eudi/openid4vp"
)

// Responder delivers the Authorization Response to the verifier's response_uri
// in response_mode=direct_post (OpenID4VP 1.0 §8.2): a form POST carrying the
// vp_token and the request's state.
type Responder struct {
	policy Policy
	client *http.Client
}

func NewResponder(policy Policy) *Responder {
	return &Responder{policy: policy, client: newClient(policy)}
}

// directPostAck is what a verifier may answer a direct_post with: where to send
// the browser next.
type directPostAck struct {
	RedirectURI string `json:"redirect_uri"`
}

// DirectPost posts the presentation and returns the verifier's redirect_uri, if
// it supplied one. The response_uri was validated when the Request Object was,
// but the policy is re-applied here: the row is the only place it lives in
// between, and a defence that depends on the row being untouched is not one.
func (r *Responder) DirectPost(ctx context.Context, responseURI string, token openid4vp.VpToken, state string) (string, error) {
	u, err := r.policy.checkURL(responseURI)
	if err != nil {
		return "", fmt.Errorf("response_uri: %w", err)
	}
	vpToken, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("marshal vp_token: %w", err)
	}
	form := url.Values{"vp_token": {string(vpToken)}}
	if state != "" {
		form.Set("state", state)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	body, err := do(r.client, req)
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", nil
	}
	var ack directPostAck
	if err := json.Unmarshal(body, &ack); err != nil {
		// An acknowledgement that is not JSON is still an acknowledgement; only the
		// optional redirect is lost.
		return "", nil
	}
	if ack.RedirectURI == "" {
		return "", nil
	}
	// The browser is sent here; refuse anything that is not an absolute http(s)
	// URL so a verifier cannot hand back a javascript: or data: target.
	redirect, err := url.Parse(ack.RedirectURI)
	if err != nil || !redirect.IsAbs() || (redirect.Scheme != "https" && redirect.Scheme != "http") {
		return "", nil
	}
	return redirect.String(), nil
}
