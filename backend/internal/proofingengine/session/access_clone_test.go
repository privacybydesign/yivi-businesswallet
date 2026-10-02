package session

import (
	"testing"
	"time"
)

func TestAccessCloneIsDeep(t *testing.T) {
	orig := Access{
		Native:  &DeviceAccess{ID: "d1"},
		History: []DeviceParticipation{{DeviceID: "d1", Steps: make([]string, 0, 4)}},
		Revoked: []string{"h1"},
	}
	c := orig.Clone()
	c.Native.LastActiveAt = time.Now()
	c.RecordStep(DeviceRoleNative, "nfc_read")
	c.Revoked[0] = "changed"
	if !orig.Native.LastActiveAt.IsZero() || len(orig.History[0].Steps) != 0 || orig.Revoked[0] != "h1" {
		t.Fatalf("mutating the clone changed the original: %+v", orig)
	}
	if len(c.History[0].Steps) != 1 {
		t.Fatalf("clone History steps = %v, want [nfc_read]", c.History[0].Steps)
	}
}
