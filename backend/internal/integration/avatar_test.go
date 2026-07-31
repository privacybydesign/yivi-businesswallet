//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type avatarBody struct {
	AvatarURI string `json:"avatarUri"`
}

func TestAvatarUploadIsServedAndReportedByMe(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")

	if me := env.getMe(t); me.AvatarURI != "" {
		t.Fatalf("avatarUri = %q before any upload, want empty", me.AvatarURI)
	}

	uploaded := env.uploadAvatar(t, http.StatusOK)
	if uploaded.AvatarURI == "" {
		t.Fatal("upload returned an empty avatarUri")
	}
	if me := env.getMe(t); me.AvatarURI != uploaded.AvatarURI {
		t.Errorf("/me avatarUri = %q, want the uploaded %q", me.AvatarURI, uploaded.AvatarURI)
	}

	resp := env.do(http.MethodGet, "/api/v1/me/avatar", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me/avatar = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	// The response is the server's own re-encode, so it decodes as a PNG but need
	// not be byte-identical to what was uploaded.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read avatar body: %v", err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(body)); err != nil || format != "png" {
		t.Errorf("served body decodes as (%q, %v), want a png", format, err)
	}
}

func TestAvatarRemovalClearsTheURI(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")
	env.uploadAvatar(t, http.StatusOK)

	resp := env.do(http.MethodDelete, "/api/v1/me/avatar", nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /me/avatar = %d, want 200", resp.StatusCode)
	}

	if me := env.getMe(t); me.AvatarURI != "" {
		t.Errorf("avatarUri = %q after removal, want empty", me.AvatarURI)
	}
	after := env.do(http.MethodGet, "/api/v1/me/avatar", nil)
	_ = after.Body.Close()
	if after.StatusCode != http.StatusNotFound {
		t.Errorf("GET /me/avatar after removal = %d, want 404", after.StatusCode)
	}
}

func TestAvatarRejectsANonImage(t *testing.T) {
	env := setup(t)
	env.login("alice@example.test")

	body, contentType := avatarForm(t, []byte("this is plainly not an image file"))
	resp := env.doWithContentType(http.MethodPut, "/api/v1/me/avatar", body, contentType)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT /me/avatar with a text file = %d, want 400", resp.StatusCode)
	}
}

// A member's photo is readable by an administrator of an organisation they belong
// to, and by nobody else — the route is what enforces that.
func TestMemberAvatarIsServedToOrgAdminsOnly(t *testing.T) {
	env := setup(t)

	memberID := env.login("member@example.test")
	env.uploadAvatar(t, http.StatusOK)

	sharedOrg := env.createOrg("Acme", "acme")
	otherOrg := env.createOrg("Globex", "globex")
	env.addMembership(memberID.ID, sharedOrg, organization.RoleMember)

	adminID := env.createUser("admin@example.test")
	env.addMembership(adminID, sharedOrg, organization.RoleAdmin)
	env.addMembership(adminID, otherOrg, organization.RoleAdmin)
	env.login("admin@example.test")

	path := "/api/v1/orgs/acme/members/" + memberID.ID.String() + "/avatar"
	resp := env.do(http.MethodGet, path, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin of the shared org GET %s = %d, want 200", path, resp.StatusCode)
	}

	// The same photo, asked for under an org the member is not in: not found.
	outside := "/api/v1/orgs/globex/members/" + memberID.ID.String() + "/avatar"
	respOutside := env.do(http.MethodGet, outside, nil)
	_ = respOutside.Body.Close()
	if respOutside.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s = %d, want 404 for a non-member of that org", outside, respOutside.StatusCode)
	}

	// A plain member of the org may not read another member's photo.
	env.login("member@example.test")
	respMember := env.do(http.MethodGet, path, nil)
	_ = respMember.Body.Close()
	if respMember.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin GET %s = %d, want 403", path, respMember.StatusCode)
	}
}

func TestAuditLogCarriesTheActorAvatar(t *testing.T) {
	env := setup(t)

	admin := env.login("admin@example.test")
	env.uploadAvatar(t, http.StatusOK)
	org := env.createOrg("Acme", "acme")
	env.addMembership(admin.ID, org, organization.RoleAdmin)

	// Any audited action attributed to the admin will do; creating a department is
	// the cheapest one this router exposes.
	dept := env.do(http.MethodPost, "/api/v1/orgs/acme/departments",
		bytes.NewReader([]byte(`{"name":"Engineering"}`)))
	_ = dept.Body.Close()
	if dept.StatusCode != http.StatusCreated {
		t.Fatalf("create department = %d, want 201", dept.StatusCode)
	}

	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/audit-events", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET audit-events = %d, want 200", resp.StatusCode)
	}
	var page struct {
		Events []struct {
			Actor *struct {
				UserID    uuid.UUID `json:"userId"`
				AvatarURI string    `json:"avatarUri"`
			} `json:"actor"`
		} `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode audit events: %v", err)
	}

	want := "/api/v1/orgs/acme/members/" + admin.ID.String() + "/avatar"
	found := false
	for _, event := range page.Events {
		if event.Actor == nil || event.Actor.UserID != admin.ID {
			continue
		}
		found = true
		if !strings.HasPrefix(event.Actor.AvatarURI, want) {
			t.Errorf("actor avatarUri = %q, want it to start with %q", event.Actor.AvatarURI, want)
		}
	}
	if !found {
		t.Error("no audit event attributed to the admin who created the department")
	}
}

// uploadAvatar PUTs a small PNG as the logged-in user's photo.
func (e *testEnv) uploadAvatar(t *testing.T, wantStatus int) avatarBody {
	t.Helper()
	body, contentType := avatarForm(t, testPNG(t))
	resp := e.doWithContentType(http.MethodPut, "/api/v1/me/avatar", body, contentType)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Fatalf("PUT /me/avatar = %d, want %d", resp.StatusCode, wantStatus)
	}
	var out avatarBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	return out
}

func (e *testEnv) doWithContentType(method, path string, body *bytes.Buffer, contentType string) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.server.URL+path, body)
	if err != nil {
		e.t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

func avatarForm(t *testing.T, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("photo", "avatar")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 0x30, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
