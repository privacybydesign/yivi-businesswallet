package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakeAvatarStore records what the handler asked it to store, so the tests can
// assert on the normalised bytes rather than on the raw upload.
type fakeAvatarStore struct {
	avatar    user.Avatar
	getErr    error
	stored    *user.Avatar
	cleared   bool
	updatedAt time.Time
}

func (f *fakeAvatarStore) GetAvatar(context.Context, uuid.UUID) (user.Avatar, error) {
	return f.avatar, f.getErr
}

func (f *fakeAvatarStore) SetAvatar(_ context.Context, id uuid.UUID, a user.Avatar) (user.User, error) {
	f.stored = &a
	return user.User{ID: id, HasAvatar: true, AvatarUpdatedAt: &f.updatedAt}, nil
}

func (f *fakeAvatarStore) ClearAvatar(_ context.Context, id uuid.UUID) (user.User, error) {
	f.cleared = true
	return user.User{ID: id}, nil
}

func avatarHandler(store *fakeAvatarStore) *Handler {
	return &Handler{users: store, admins: NewPlatformAdmins(nil)}
}

func withUser(r *http.Request) *http.Request {
	return r.WithContext(ContextWithUser(r.Context(), user.User{ID: uuid.New(), Email: "someone@example.org"}))
}

func samplePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// uploadRequest builds a multipart PUT carrying content in the named form field.
func uploadRequest(t *testing.T, field string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if field != "" {
		part, err := form.CreateFormFile(field, "photo.png")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return withUser(req)
}

func apiErrorFrom(t *testing.T, err error) *respond.APIError {
	t.Helper()
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *respond.APIError", err)
	}
	return apiErr
}

func TestPutMyAvatarStoresNormalisedImage(t *testing.T) {
	store := &fakeAvatarStore{updatedAt: time.Unix(1700000000, 0)}
	rec := httptest.NewRecorder()

	if err := avatarHandler(store).putMyAvatar(rec, uploadRequest(t, "avatar", samplePNG(t, 300, 200))); err != nil {
		t.Fatalf("putMyAvatar: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if store.stored == nil {
		t.Fatal("nothing was stored")
	}
	// The upload was a PNG; what is stored is the re-encoded square JPEG.
	if store.stored.ContentType != user.AvatarContentType {
		t.Errorf("stored content type = %q, want %q", store.stored.ContentType, user.AvatarContentType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(store.stored.Bytes))
	if err != nil {
		t.Fatalf("decode stored avatar: %v", err)
	}
	if cfg.Width != user.AvatarSize || cfg.Height != user.AvatarSize {
		t.Errorf("stored size = %dx%d, want %dx%d", cfg.Width, cfg.Height, user.AvatarSize, user.AvatarSize)
	}

	var body meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := MeAvatarPath + "?v=1700000000"; body.AvatarURI != want {
		t.Errorf("avatarUri = %q, want %q", body.AvatarURI, want)
	}
}

func TestPutMyAvatarRejectsNonImage(t *testing.T) {
	store := &fakeAvatarStore{}

	err := avatarHandler(store).putMyAvatar(httptest.NewRecorder(),
		uploadRequest(t, "avatar", []byte("definitely not a photo")))

	if got := apiErrorFrom(t, err).Status; got != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", got)
	}
	if store.stored != nil {
		t.Error("a rejected upload was stored anyway")
	}
}

func TestPutMyAvatarRejectsMissingAndEmptyFile(t *testing.T) {
	cases := map[string]*http.Request{
		"no file part": uploadRequest(t, "", nil),
		"empty file":   uploadRequest(t, "avatar", nil),
	}
	for name, req := range cases {
		err := avatarHandler(&fakeAvatarStore{}).putMyAvatar(httptest.NewRecorder(), req)
		if got := apiErrorFrom(t, err).Status; got != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, got)
		}
	}
}

func TestPutMyAvatarRejectsOversizedUpload(t *testing.T) {
	oversized := bytes.Repeat([]byte{0x42}, user.MaxAvatarUploadBytes+1)

	err := avatarHandler(&fakeAvatarStore{}).putMyAvatar(httptest.NewRecorder(),
		uploadRequest(t, "avatar", oversized))

	if got := apiErrorFrom(t, err).Status; got != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", got)
	}
}

func TestDeleteMyAvatarClearsAndReportsNoAvatar(t *testing.T) {
	store := &fakeAvatarStore{}
	rec := httptest.NewRecorder()

	if err := avatarHandler(store).deleteMyAvatar(rec, withUser(httptest.NewRequest(http.MethodDelete, "/me/avatar", nil))); err != nil {
		t.Fatalf("deleteMyAvatar: %v", err)
	}

	if !store.cleared {
		t.Error("the store was not asked to clear the avatar")
	}
	var body meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AvatarURI != "" {
		t.Errorf("avatarUri = %q, want empty", body.AvatarURI)
	}
}

func TestServeMyAvatarNotFoundWithoutOne(t *testing.T) {
	store := &fakeAvatarStore{getErr: user.ErrNoAvatar}

	err := avatarHandler(store).serveMyAvatar(httptest.NewRecorder(),
		withUser(httptest.NewRequest(http.MethodGet, "/me/avatar", nil)))

	if got := apiErrorFrom(t, err).Status; got != http.StatusNotFound {
		t.Errorf("status = %d, want 404", got)
	}
}

func TestServeMyAvatarStreamsTheStoredBytes(t *testing.T) {
	stored := user.Avatar{Bytes: []byte{0xFF, 0xD8, 0xFF}, ContentType: user.AvatarContentType}
	rec := httptest.NewRecorder()

	if err := avatarHandler(&fakeAvatarStore{avatar: stored}).serveMyAvatar(rec,
		withUser(httptest.NewRequest(http.MethodGet, "/me/avatar", nil))); err != nil {
		t.Fatalf("serveMyAvatar: %v", err)
	}

	if !bytes.Equal(rec.Body.Bytes(), stored.Bytes) {
		t.Error("body does not match the stored avatar")
	}
	if got := rec.Header().Get("Content-Type"); got != user.AvatarContentType {
		t.Errorf("Content-Type = %q, want %q", got, user.AvatarContentType)
	}
}
