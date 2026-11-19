package auth

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

func TestSelfReviewOnlyAPlatformAdminInDevMode(t *testing.T) {
	admin, member := user.Email("root@example.org"), user.Email("sam@example.org")
	admins := NewPlatformAdmins([]string{string(admin)})
	if !NewSelfReview(true, admins).Allows(admin) {
		t.Error("a platform admin in DEV_MODE may not decide their own check")
	}
	if NewSelfReview(true, admins).Allows(member) {
		t.Error("a member in DEV_MODE may decide their own check")
	}
	if NewSelfReview(false, admins).Allows(admin) {
		t.Error("a platform admin outside DEV_MODE may decide their own check")
	}
	if (SelfReview{}).Allows(admin) {
		t.Error("the zero value allows someone")
	}
}
