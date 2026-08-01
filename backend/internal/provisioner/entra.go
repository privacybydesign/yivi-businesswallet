package provisioner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	// DefaultGraphBaseURL is the Microsoft Graph v1.0 endpoint. v1.0 rather than
	// beta: the beta endpoint carries no compatibility promise and Microsoft
	// changes it without notice.
	DefaultGraphBaseURL = "https://graph.microsoft.com/v1.0"
	// DefaultLoginBaseURL is the Entra token endpoint host.
	DefaultLoginBaseURL = "https://login.microsoftonline.com"

	// graphScope requests the app-registration's own consented application
	// permissions (User.Read.All, GroupMember.Read.All) rather than a user's.
	// Provisioning runs on a schedule with nobody signed in, so the
	// client-credentials flow is the only one available.
	graphScope = "https://graph.microsoft.com/.default"

	// personSelect asks Graph for exactly the attributes our membership model
	// carries. Selecting explicitly is not just economy: the default user
	// projection returns a good deal more personal data than we have any reason to
	// receive, and this is a directory of real people.
	personSelect = "id,mail,userPrincipalName,givenName,surname,jobTitle,department,accountEnabled"

	// graphPageSize is the largest page Graph serves for a user collection.
	graphPageSize = 999

	// maxPages bounds a paged read. A directory that keeps handing out a next link
	// (or a misconfigured endpoint that loops) would otherwise pin the sync
	// goroutine forever; 100 pages of 999 is far past any organisation that would
	// be run out of one business wallet.
	maxPages = 100

	// maxErrorBody bounds how much of a failed response is read into an error
	// message.
	maxErrorBody = 4 << 10
)

// Entra provisions from Microsoft Entra ID (formerly Azure AD) by polling
// Microsoft Graph.
//
// Entra offers two standard integration shapes and this is the pull one: we ask
// Graph for the people in scope and reconcile on our side. The push shape (SCIM
// 2.0, where Entra's provisioning service calls an endpoint we expose) was not
// chosen for the first source, for two reasons. It would have to be an
// unauthenticated-to-our-session, token-authenticated write endpoint that can
// create and remove memberships, which is a much larger thing to get right than
// an outbound read; and it inverts the seam — a SCIM server is not a Provisioner
// any other source could implement, it is a second way in. See
// .ai/features/provisioning.md for the full argument and what would have to
// change to add SCIM alongside this.
type Entra struct {
	graphBaseURL string
	loginBaseURL string
	http         *http.Client
}

// NewEntra builds the Graph driver against the public Microsoft endpoints.
func NewEntra(client *http.Client) *Entra {
	return &Entra{
		graphBaseURL: DefaultGraphBaseURL,
		loginBaseURL: DefaultLoginBaseURL,
		http:         client,
	}
}

// WithEndpoints points the driver at different Graph and login hosts. It exists
// for tests and for a sovereign cloud deployment (Graph has per-region hosts);
// both URLs are used as prefixes, with no trailing slash.
func (e *Entra) WithEndpoints(graphBaseURL, loginBaseURL string) *Entra {
	e.graphBaseURL = strings.TrimRight(graphBaseURL, "/")
	e.loginBaseURL = strings.TrimRight(loginBaseURL, "/")
	return e
}

func (e *Entra) ID() SourceID { return SourceEntra }

// Fetch reads the people in scope, resolves who is an admin, and returns the
// snapshot. Every request carries ctx, so the caller's deadline actually bounds
// the sync rather than only the first call.
func (e *Entra) Fetch(ctx context.Context, cfg Config) (Directory, error) {
	if cfg.TenantID == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return Directory{}, ErrIncompleteConfig
	}

	token, err := e.token(ctx, cfg)
	if err != nil {
		return Directory{}, err
	}

	people, err := e.people(ctx, token, cfg.GroupID)
	if err != nil {
		return Directory{}, err
	}

	admins, err := e.adminIDs(ctx, token, cfg.AdminGroupIDs)
	if err != nil {
		return Directory{}, err
	}
	for i := range people {
		people[i].Admin = admins[people[i].ExternalID]
	}

	return Directory{People: people}, nil
}

// token exchanges the app registration's client credentials for a Graph access
// token.
func (e *Entra) token(ctx context.Context, cfg Config) (string, error) {
	form := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"grant_type":    {"client_credentials"},
		"scope":         {graphScope},
	}
	endpoint := e.loginBaseURL + "/" + url.PathEscape(cfg.TenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("provisioner: entra token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := e.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("provisioner: entra token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("provisioner: entra token: %s", statusDetail(resp))
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("provisioner: entra token: decode response: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("provisioner: entra token: response carried no access token")
	}
	return body.AccessToken, nil
}

// graphPerson is the Graph user projection personSelect asks for.
type graphPerson struct {
	ID                string `json:"id"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
	GivenName         string `json:"givenName"`
	Surname           string `json:"surname"`
	JobTitle          string `json:"jobTitle"`
	Department        string `json:"department"`
	// AccountEnabled is a pointer so an absent field is distinguishable from
	// false. Graph omits it for a directory object the app registration may list
	// but not read in full, and treating "not told" as "disabled" would
	// deprovision the whole organisation.
	AccountEnabled *bool `json:"accountEnabled"`
}

// people reads the accounts in scope: the members of groupID, or the whole
// directory when it is empty.
func (e *Entra) people(ctx context.Context, token, groupID string) ([]Person, error) {
	endpoint := e.collectionURL(groupID, personSelect)

	var people []Person
	err := e.eachPage(ctx, token, endpoint, func(raw json.RawMessage) error {
		var page []graphPerson
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("provisioner: entra users: decode page: %w", err)
		}
		for _, u := range page {
			people = append(people, Person{
				ExternalID: u.ID,
				Email:      strings.TrimSpace(firstNonEmpty(u.Mail, u.UserPrincipalName)),
				GivenNames: strings.TrimSpace(u.GivenName),
				LastName:   strings.TrimSpace(u.Surname),
				JobTitle:   strings.TrimSpace(u.JobTitle),
				Department: strings.TrimSpace(u.Department),
				Active:     u.AccountEnabled == nil || *u.AccountEnabled,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return people, nil
}

// adminIDs resolves the object ids of everyone in the configured admin groups.
// Reading each group's membership costs one paged call per group, where asking
// per person would cost one per person.
func (e *Entra) adminIDs(ctx context.Context, token string, groupIDs []string) (map[string]bool, error) {
	admins := map[string]bool{}
	for _, groupID := range groupIDs {
		if strings.TrimSpace(groupID) == "" {
			continue
		}
		endpoint := e.collectionURL(groupID, "id")
		err := e.eachPage(ctx, token, endpoint, func(raw json.RawMessage) error {
			var page []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				return fmt.Errorf("provisioner: entra admin group members: decode page: %w", err)
			}
			for _, m := range page {
				admins[m.ID] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return admins, nil
}

// collectionURL is the first page of a user collection: the members of a group,
// or every user when groupID is empty. The /microsoft.graph.user segment is an
// OData cast — a group's members can be groups or service principals too, and
// the cast makes Graph return only the people.
func (e *Entra) collectionURL(groupID, selection string) string {
	query := url.Values{
		"$select": {selection},
		"$top":    {fmt.Sprint(graphPageSize)},
	}.Encode()
	if groupID == "" {
		return e.graphBaseURL + "/users?" + query
	}
	return e.graphBaseURL + "/groups/" + url.PathEscape(groupID) + "/members/microsoft.graph.user?" + query
}

// eachPage walks an OData collection, handing each page's raw value array to fn
// and following @odata.nextLink.
func (e *Entra) eachPage(ctx context.Context, token, endpoint string, fn func(json.RawMessage) error) error {
	for range maxPages {
		var body struct {
			Value    json.RawMessage `json:"value"`
			NextLink string          `json:"@odata.nextLink"`
		}
		if err := e.get(ctx, token, endpoint, &body); err != nil {
			return err
		}
		if err := fn(body.Value); err != nil {
			return err
		}
		if body.NextLink == "" {
			return nil
		}
		// The next link is a URL the response chose. Following it blindly would let
		// a compromised or spoofed response walk our bearer token to a host of its
		// choosing, so it has to stay on the Graph endpoint we were pointed at.
		if !strings.HasPrefix(body.NextLink, e.graphBaseURL+"/") {
			return fmt.Errorf("provisioner: entra: next page link left the graph endpoint")
		}
		endpoint = body.NextLink
	}
	return fmt.Errorf("provisioner: entra: more than %d pages", maxPages)
}

func (e *Entra) get(ctx context.Context, token, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("provisioner: entra request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("provisioner: entra request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provisioner: entra request: %s", statusDetail(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("provisioner: entra request: decode response: %w", err)
	}
	return nil
}

// statusDetail renders a failed response as status plus Graph's own error code,
// which is what tells an admin whether they granted the wrong permission or
// typed the wrong tenant. The message is bounded and the raw body is not
// repeated: a token endpoint error can echo request parameters back.
func statusDetail(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	var graphShape struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &graphShape)
	// The token endpoint answers in the OAuth shape instead of Graph's, where
	// "error" is a string. The two cannot share one struct: two fields tagged with
	// the same name make encoding/json ignore both.
	var oauthShape struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &oauthShape)

	code := firstNonEmpty(graphShape.Error.Code, oauthShape.Error)
	if code == "" {
		return fmt.Sprintf("status %d", resp.StatusCode)
	}
	return fmt.Sprintf("status %d (%s)", resp.StatusCode, code)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
