package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphServer is a stand-in for the Entra token endpoint plus Microsoft Graph.
// It records what was asked for so a test can assert the driver requested the
// right thing, not only that it parsed the answer.
type graphServer struct {
	t *testing.T

	tokenForm  map[string]string
	requested  []string
	tokenValue string
	baseURL    string

	// pages keyed by request path+query; each page is a raw JSON body.
	pages map[string]string
	// status keyed by request path; anything but 0 is returned instead of a page.
	status map[string]int
	body   map[string]string
}

func newGraphServer(t *testing.T) (*graphServer, *Entra) {
	t.Helper()
	g := &graphServer{
		t:          t,
		tokenValue: "graph-token",
		pages:      map[string]string{},
		status:     map[string]int{},
		body:       map[string]string{},
	}
	server := httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(server.Close)
	// Both endpoints on one server: the driver keeps them apart by path, and the
	// test only cares that it addressed the right one.
	entra := NewEntra(server.Client()).WithEndpoints(server.URL+"/graph", server.URL+"/login")
	g.baseURL = server.URL
	return g, entra
}

func (g *graphServer) serve(w http.ResponseWriter, r *http.Request) {
	key := r.URL.RequestURI()
	if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
		if err := r.ParseForm(); err != nil {
			g.t.Errorf("parse token form: %v", err)
		}
		g.tokenForm = map[string]string{}
		for name := range r.Form {
			g.tokenForm[name] = r.Form.Get(name)
		}
		g.requested = append(g.requested, r.URL.Path)
		if status, ok := g.status[r.URL.Path]; ok {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(g.body[r.URL.Path]))
			return
		}
		writeJSON(w, map[string]any{"access_token": g.tokenValue, "expires_in": 3600})
		return
	}

	if got := r.Header.Get("Authorization"); got != "Bearer "+g.tokenValue {
		g.t.Errorf("Authorization = %q, want bearer %q", got, g.tokenValue)
	}
	g.requested = append(g.requested, key)
	if status, ok := g.status[r.URL.Path]; ok {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(g.body[r.URL.Path]))
		return
	}
	page, ok := g.pages[key]
	if !ok {
		g.t.Errorf("unexpected request %q", key)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(page))
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (g *graphServer) asked(prefix string) bool {
	for _, r := range g.requested {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func TestEntraFetchMapsPeopleAndResolvesAdmins(t *testing.T) {
	g, entra := newGraphServer(t)

	groupPath := "/graph/groups/staff-group/members/microsoft.graph.user"
	g.pages[groupPath+"?%24select="+selectEscaped(personSelect)+"&%24top=999"] = `{
		"value": [
			{"id": "u1", "mail": "Ada@example.org", "givenName": " Ada ", "surname": "Lovelace",
			 "jobTitle": " Engineer ", "department": " Research ", "accountEnabled": true},
			{"id": "u2", "userPrincipalName": "bob@example.org", "givenName": "Bob", "surname": "Baker",
			 "accountEnabled": false}
		]
	}`
	g.pages["/graph/groups/admins/members/microsoft.graph.user?%24select=id&%24top=999"] = `{"value": [{"id": "u1"}]}`

	directory, err := entra.Fetch(context.Background(), Config{
		TenantID:      "tenant",
		ClientID:      "client",
		ClientSecret:  "secret",
		GroupID:       "staff-group",
		AdminGroupIDs: []string{"admins", "  "},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(directory.People) != 2 {
		t.Fatalf("people = %d, want 2", len(directory.People))
	}
	ada := directory.People[0]
	want := Person{
		ExternalID: "u1", Email: "Ada@example.org", GivenNames: "Ada", LastName: "Lovelace",
		JobTitle: "Engineer", Department: "Research", Admin: true, Active: true,
	}
	if ada != want {
		t.Errorf("person = %+v, want %+v", ada, want)
	}

	bob := directory.People[1]
	// userPrincipalName is the fallback when the account has no mail attribute,
	// which is the common shape for a guest or an unlicensed account.
	if bob.Email != "bob@example.org" {
		t.Errorf("email = %q, want the userPrincipalName fallback", bob.Email)
	}
	if bob.Active {
		t.Error("a disabled account should be reported inactive, not left out")
	}
	if bob.Admin {
		t.Error("bob is in no admin group")
	}

	if g.tokenForm["grant_type"] != "client_credentials" || g.tokenForm["scope"] != graphScope {
		t.Errorf("token form = %v, want a client-credentials grant for the graph scope", g.tokenForm)
	}
	if g.asked("/graph/users") {
		t.Error("a configured group must scope the read; the whole directory was requested")
	}
}

func TestEntraFetchWithoutGroupReadsTheWholeDirectory(t *testing.T) {
	g, entra := newGraphServer(t)
	g.pages["/graph/users?%24select="+selectEscaped(personSelect)+"&%24top=999"] = `{"value": [{"id": "u1", "mail": "ada@example.org", "givenName": "Ada", "surname": "Lovelace"}]}`

	directory, err := entra.Fetch(context.Background(), Config{TenantID: "t", ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(directory.People) != 1 {
		t.Fatalf("people = %d, want 1", len(directory.People))
	}
	// accountEnabled absent must not read as disabled: Graph omits it for objects
	// the app registration may list but not read in full, and treating that as
	// "disabled" would deprovision the whole organisation.
	if !directory.People[0].Active {
		t.Error("an account with no accountEnabled field should be treated as active")
	}
}

func TestEntraFetchFollowsPaging(t *testing.T) {
	g, entra := newGraphServer(t)
	first := "/graph/users?%24select=" + selectEscaped(personSelect) + "&%24top=999"
	next := "/graph/users?%24skiptoken=page2"
	g.pages[first] = fmt.Sprintf(
		`{"value": [{"id": "u1", "mail": "a@example.org", "givenName": "A", "surname": "A"}], "@odata.nextLink": %q}`,
		"BASE"+next)
	g.pages[next] = `{"value": [{"id": "u2", "mail": "b@example.org", "givenName": "B", "surname": "B"}]}`

	// The next link has to be an absolute URL on the same Graph endpoint.
	g.pages[first] = strings.Replace(g.pages[first], "BASE", g.baseURL, 1)

	directory, err := entra.Fetch(context.Background(), Config{TenantID: "t", ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(directory.People) != 2 {
		t.Fatalf("people = %d, want both pages", len(directory.People))
	}
}

func TestEntraFetchRefusesANextLinkOffTheGraphEndpoint(t *testing.T) {
	g, entra := newGraphServer(t)
	first := "/graph/users?%24select=" + selectEscaped(personSelect) + "&%24top=999"
	// A response that redirects paging at somebody else's host would walk our
	// bearer token there.
	g.pages[first] = `{"value": [], "@odata.nextLink": "https://attacker.example/graph/users"}`

	_, err := entra.Fetch(context.Background(), Config{TenantID: "t", ClientID: "c", ClientSecret: "s"})
	if err == nil || !strings.Contains(err.Error(), "left the graph endpoint") {
		t.Fatalf("err = %v, want a refusal to follow the link", err)
	}
}

func TestEntraFetchRequiresCredentials(t *testing.T) {
	_, entra := newGraphServer(t)
	for name, cfg := range map[string]Config{
		"no tenant": {ClientID: "c", ClientSecret: "s"},
		"no client": {TenantID: "t", ClientSecret: "s"},
		"no secret": {TenantID: "t", ClientID: "c"},
	} {
		if _, err := entra.Fetch(context.Background(), cfg); !errors.Is(err, ErrIncompleteConfig) {
			t.Errorf("%s: err = %v, want ErrIncompleteConfig", name, err)
		}
	}
}

func TestEntraFetchReportsTheSourceErrorCode(t *testing.T) {
	g, entra := newGraphServer(t)
	g.status["/graph/users"] = http.StatusForbidden
	g.body["/graph/users"] = `{"error": {"code": "Authorization_RequestDenied", "message": "Insufficient privileges"}}`

	_, err := entra.Fetch(context.Background(), Config{TenantID: "t", ClientID: "c", ClientSecret: "s"})
	if err == nil {
		t.Fatal("Fetch succeeded on a 403")
	}
	// The code is what tells an admin they granted the wrong permission, so it has
	// to survive into the message the settings screen shows.
	if !strings.Contains(err.Error(), "Authorization_RequestDenied") || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want the status and the source's error code", err)
	}
}

func TestEntraFetchDoesNotLeakTheClientSecret(t *testing.T) {
	g, entra := newGraphServer(t)
	g.status["/login/tenant/oauth2/v2.0/token"] = http.StatusUnauthorized
	g.body["/login/tenant/oauth2/v2.0/token"] = `{"error": "invalid_client", "error_description": "AADSTS7000215: Invalid client secret hunter2 provided."}`

	_, err := entra.Fetch(context.Background(), Config{
		TenantID: "tenant", ClientID: "c", ClientSecret: "hunter2",
	})
	if err == nil {
		t.Fatal("Fetch succeeded on a rejected token request")
	}
	// The error is stored on the settings row and shown to org admins, and the
	// token endpoint happily echoes request material back in its description.
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("err = %v, must not repeat the client secret", err)
	}
	if !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("err = %v, want the OAuth error code", err)
	}
}

// selectEscaped renders a $select value as it appears in an encoded query, so a
// test can key a stub page by the URL the driver actually builds.
func selectEscaped(selection string) string {
	return strings.ReplaceAll(selection, ",", "%2C")
}
