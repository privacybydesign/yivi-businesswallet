// Package useravatar stores and serves the portrait photo a user sets for
// themselves. The avatar is per-user (global identity), not per-org: one photo
// follows the person into every organisation they are a member of.
//
// It is personal data, so exposure is kept to the people who already see the
// person: the user themselves (/me/avatar) and administrators of an organisation
// the user is a member of (/orgs/{slug}/members/{userId}/avatar). Uploads are
// re-encoded before storage, which strips the EXIF block a phone photo carries —
// GPS coordinates included.
package useravatar

import (
	"errors"
	"time"
)

const (
	// MaxAvatarBytes caps a stored avatar. An avatar renders at 28-80px, so a
	// 512x512 JPEG (tens of KiB) is already more than the UI can use; 512 KiB
	// matches the theme logo cap and leaves room for a lossless PNG portrait.
	MaxAvatarBytes = 512 << 10
	// MaxAvatarDimension caps either side of the uploaded image. The frontend
	// downscales before uploading, so this is the guard against a hand-crafted
	// request, not the normal path.
	MaxAvatarDimension = 1024
)

// ErrNoAvatar is returned when the user has no stored avatar (or, for a member
// lookup, is not a member of the organisation asking).
var ErrNoAvatar = errors.New("useravatar: no avatar")

// Avatar is a stored avatar image. UpdatedAt is the cache-busting version in the
// URL the image is served from.
type Avatar struct {
	Bytes       []byte
	ContentType string
	UpdatedAt   time.Time
}
