package useravatar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

type fakeStore struct {
	avatar    Avatar
	getErr    error
	savedAt   time.Time
	saved     *Avatar
	deleted   bool
	memberErr error
}

func (f *fakeStore) Get(context.Context, uuid.UUID) (Avatar, error) {
	return f.avatar, f.getErr
}

func (f *fakeStore) GetForOrgMember(context.Context, uuid.UUID, uuid.UUID) (Avatar, error) {
	return f.avatar, f.memberErr
}

func (f *fakeStore) Save(_ context.Context, _ uuid.UUID, a Avatar) (time.Time, error) {
	f.saved = &a
	return f.savedAt, nil
}

func (f *fakeStore) Delete(context.Context, uuid.UUID) error {
	f.deleted = true
	return nil
}

func requestAsUser(t *testing.T, method, target string, body *bytes.Buffer) *http.Request {
	t.Helper()
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
	}
	ctx := auth.ContextWithUser(req.Context(), user.User{ID: uuid.New()})
	return req.WithContext(ctx)
}

func TestSetAvatarResponseHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	setAvatarResponseHeaders(rec.Header(), "image/jpeg")

	want := map[string]string{
		"Content-Type":            "image/jpeg",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
		// The photo is personal data, so no shared cache may keep a copy.
		"Cache-Control": "private, max-age=300",
	}
	for header, wantValue := range want {
		if got := rec.Header().Get(header); got != wantValue {
			t.Errorf("%s = %q, want %q", header, got, wantValue)
		}
	}
}

func TestServeMineNotFoundWithoutAvatar(t *testing.T) {
	h := &Handler{store: &fakeStore{getErr: ErrNoAvatar}}

	err := h.serveMine(httptest.NewRecorder(), requestAsUser(t, http.MethodGet, "/me/avatar", nil))

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want 404 APIError", err)
	}
}

func TestServeMineWritesStoredBytes(t *testing.T) {
	stored := Avatar{Bytes: []byte{1, 2, 3}, ContentType: "image/png"}
	h := &Handler{store: &fakeStore{avatar: stored}}
	rec := httptest.NewRecorder()

	if err := h.serveMine(rec, requestAsUser(t, http.MethodGet, "/me/avatar", nil)); err != nil {
		t.Fatalf("serveMine: %v", err)
	}
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), stored.Bytes) {
		t.Errorf("response = %d %v, want 200 %v", rec.Code, rec.Body.Bytes(), stored.Bytes)
	}
	if got := rec.Header().Get("Content-Type"); got != stored.ContentType {
		t.Errorf("Content-Type = %q, want %q", got, stored.ContentType)
	}
}

func TestServeMemberRejectsNonUUID(t *testing.T) {
	h := &Handler{store: &fakeStore{}}
	req := httptest.NewRequest(http.MethodGet, "/orgs/acme/members/not-a-uuid/avatar", nil)
	req.SetPathValue("userId", "not-a-uuid")

	err := h.serveMember(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_user_id" {
		t.Fatalf("err = %v, want invalid_user_id APIError", err)
	}
}

func TestPutMineStoresNormalizedPhotoAndReturnsVersionedURI(t *testing.T) {
	savedAt := time.Unix(1_700_000_000, 0)
	store := &fakeStore{savedAt: savedAt}
	h := &Handler{store: store}

	body, contentType := multipartPhoto(t, "portrait.png", pngBytes(t, 64))
	req := requestAsUser(t, http.MethodPut, "/me/avatar", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	if err := h.putMine(rec, req); err != nil {
		t.Fatalf("putMine: %v", err)
	}
	if store.saved == nil || store.saved.ContentType != "image/png" {
		t.Fatalf("saved = %+v, want a stored image/png", store.saved)
	}
	var got avatarResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := "/api/v1/me/avatar?v=1700000000"; got.AvatarURI != want {
		t.Errorf("avatarUri = %q, want %q", got.AvatarURI, want)
	}
}

func TestDeleteMineClearsAndReturnsEmptyURI(t *testing.T) {
	store := &fakeStore{}
	h := &Handler{store: store}
	rec := httptest.NewRecorder()

	if err := h.deleteMine(rec, requestAsUser(t, http.MethodDelete, "/me/avatar", nil)); err != nil {
		t.Fatalf("deleteMine: %v", err)
	}
	if !store.deleted {
		t.Error("store.Delete was not called")
	}
	var got avatarResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AvatarURI != "" {
		t.Errorf("avatarUri = %q, want empty after removal", got.AvatarURI)
	}
}

func TestParseUploadRejectsNonMultipartBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", strings.NewReader("not a form"))

	_, err := parseUpload(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_body" {
		t.Fatalf("err = %v, want invalid_body APIError", err)
	}
}

func TestParseUploadRejectsMissingFile(t *testing.T) {
	body, contentType := multipartPhoto(t, "", nil)
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", body)
	req.Header.Set("Content-Type", contentType)

	_, err := parseUpload(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}

func TestParseUploadRejectsEmptyFile(t *testing.T) {
	body, contentType := multipartPhoto(t, "empty.png", []byte{})
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", body)
	req.Header.Set("Content-Type", contentType)

	_, err := parseUpload(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError for an empty file", err)
	}
}

// A hand-crafted request can send anything, so the byte cap is enforced on the
// server and not left to the browser that normally downscales first.
func TestParseUploadRejectsOversizedFile(t *testing.T) {
	body, contentType := multipartPhoto(t, "big.png", bytes.Repeat([]byte{0xAB}, MaxAvatarBytes+1))
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", body)
	req.Header.Set("Content-Type", contentType)

	_, err := parseUpload(httptest.NewRecorder(), req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %v, want 413 APIError", err)
	}
}

// multipartPhoto builds a multipart body with an optional photo file part,
// returning the body and its Content-Type header.
func multipartPhoto(t *testing.T, fileName string, fileData []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if fileName != "" {
		part, err := mw.CreateFormFile(photoFormField, fileName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(fileData); err != nil {
			t.Fatalf("write file data: %v", err)
		}
	} else if err := mw.WriteField("unrelated", "1"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
