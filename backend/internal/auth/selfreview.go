package auth

import "github.com/privacybydesign/yivi-businesswallet/backend/internal/user"

// SelfReview says who may decide an identity check they sent or are the
// subject of. Nobody in a deployment: there another reviewer decides. On a dev
// stack (DEV_MODE) a platform admin may, so one person can test a review end to
// end. The zero value allows nobody.
type SelfReview struct {
	devMode bool
	admins  PlatformAdmins
}

func NewSelfReview(devMode bool, admins PlatformAdmins) SelfReview {
	return SelfReview{devMode: devMode, admins: admins}
}

// Allows reports whether the user with email may decide their own check.
func (s SelfReview) Allows(email user.Email) bool {
	return s.devMode && s.admins.Has(email)
}
