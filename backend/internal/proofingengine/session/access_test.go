package session

import (
	"errors"
	"testing"
	"time"
)

// claimSlot mints role's claim grant and redeems it, returning the device token.
func claimSlot(t *testing.T, a *Access, role DeviceRole, now time.Time) string {
	t.Helper()
	grant, err := a.MintHandover(role, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, device, via, err := a.ClaimHandover(grant, now)
	if err != nil || got != role || via != DeviceViaClaim {
		t.Fatalf("claim %s = %q, %q, %v", role, got, via, err)
	}
	return device
}

func TestAccessFirstClaimThenHandoverRevokesPreviousDevice(t *testing.T) {
	now := time.Now().UTC()
	var a Access
	if a.Bound() {
		t.Fatal("fresh access should be unbound")
	}
	first := claimSlot(t, &a, DeviceRoleWeb, now)
	if role, err := a.Authorize(first); err != nil || role != DeviceRoleWeb {
		t.Fatalf("authorize first = %q, %v", role, err)
	}

	handover, err := a.MintHandover(DeviceRoleWeb, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	role, second, via, err := a.ClaimHandover(handover, now)
	if err != nil || role != DeviceRoleWeb || second == first || via != DeviceViaHandover {
		t.Fatalf("claim handover = %q, %q, %v", role, via, err)
	}
	if _, err := a.Authorize(first); !errors.Is(err, ErrDeviceHandedOver) {
		t.Fatalf("old device err = %v, want ErrDeviceHandedOver", err)
	}
	if err := a.AuthorizeAs(first, DeviceRoleWeb); !errors.Is(err, ErrDeviceHandedOver) {
		t.Fatalf("old device AuthorizeAs err = %v, want ErrDeviceHandedOver", err)
	}
	if err := a.AuthorizeAs(second, DeviceRoleNative); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatalf("AuthorizeAs for the other slot err = %v, want ErrDeviceUnauthorized", err)
	}
	if _, _, _, err := a.ClaimHandover(handover, now); !errors.Is(err, ErrHandoverUsed) {
		t.Fatalf("reused handover err = %v, want ErrHandoverUsed", err)
	}
	if _, err := a.Authorize("not-a-device"); !errors.Is(err, ErrDeviceUnauthorized) {
		t.Fatalf("unknown device err = %v, want ErrDeviceUnauthorized", err)
	}
	if a.Generation != 2 {
		t.Fatalf("generation = %d, want 2 (one per claim)", a.Generation)
	}
}

func TestAccessHistoryKeepsHandedOverDevices(t *testing.T) {
	now := time.Now().UTC()
	var a Access
	claimSlot(t, &a, DeviceRoleNative, now)
	a.RecordStep(DeviceRoleNative, "document_capture")
	a.RecordStep(DeviceRoleNative, "document_capture")
	h, _ := a.MintHandover(DeviceRoleNative, now.Add(time.Minute))
	later := now.Add(time.Second)
	if _, _, _, err := a.ClaimHandover(h, later); err != nil {
		t.Fatal(err)
	}
	a.RecordStep(DeviceRoleNative, "nfc_read")
	if len(a.History) != 2 {
		t.Fatalf("history = %+v, want both devices", a.History)
	}
	first, second := a.History[0], a.History[1]
	if first.Via != DeviceViaClaim || first.EndReason != "handed_over" || first.EndedAt == nil || !first.EndedAt.Equal(later) ||
		len(first.Steps) != 1 || first.Steps[0] != "document_capture" {
		t.Fatalf("first device = %+v", first)
	}
	if second.Via != DeviceViaHandover || second.EndedAt != nil || second.DeviceID != a.Native.ID ||
		len(second.Steps) != 1 || second.Steps[0] != "nfc_read" {
		t.Fatalf("second device = %+v", second)
	}
}

func TestAccessGrantExpiresAndIsReplacedByNewMintOfTheSameSlot(t *testing.T) {
	now := time.Now().UTC()
	var a Access
	old, _ := a.MintHandover(DeviceRoleNative, now.Add(time.Minute))
	fresh, _ := a.MintHandover(DeviceRoleNative, now.Add(time.Minute))
	if _, _, _, err := a.ClaimHandover(old, now); !errors.Is(err, ErrHandoverInvalid) {
		t.Fatalf("superseded handover err = %v, want ErrHandoverInvalid", err)
	}
	if _, _, _, err := a.ClaimHandover(fresh, now.Add(2*time.Minute)); !errors.Is(err, ErrHandoverExpired) {
		t.Fatalf("expired handover err = %v, want ErrHandoverExpired", err)
	}
	if a.Bound() {
		t.Fatal("a failed claim must not bind anything")
	}
}

func TestAccessWebAndNativeGrantsAreIndependent(t *testing.T) {
	now := time.Now().UTC()
	var a Access
	web, _ := a.MintHandover(DeviceRoleWeb, now.Add(time.Minute))
	native, _ := a.MintHandover(DeviceRoleNative, now.Add(time.Minute))
	if role, _, _, err := a.ClaimHandover(web, now); err != nil || role != DeviceRoleWeb {
		t.Fatalf("web grant after a native mint = %q, %v", role, err)
	}
	if _, err := a.MintHandover(DeviceRoleWeb, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if role, _, _, err := a.ClaimHandover(native, now); err != nil || role != DeviceRoleNative {
		t.Fatalf("native grant after a web mint = %q, %v", role, err)
	}
}

func TestAccessSlotsAreIndependent(t *testing.T) {
	now := time.Now().UTC()
	var a Access
	web := claimSlot(t, &a, DeviceRoleWeb, now)
	native := claimSlot(t, &a, DeviceRoleNative, now)
	h, _ := a.MintHandover(DeviceRoleNative, now.Add(time.Minute))
	if _, _, _, err := a.ClaimHandover(h, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authorize(native); !errors.Is(err, ErrDeviceHandedOver) {
		t.Fatalf("handed-over native err = %v", err)
	}
	if role, err := a.Authorize(web); err != nil || role != DeviceRoleWeb {
		t.Fatalf("web device must survive a native handover: %q, %v", role, err)
	}
	if err := a.SetDeviceState(DeviceRoleWeb, "sleepy", now); !errors.Is(err, ErrInvalidDeviceState) {
		t.Fatalf("bad state err = %v", err)
	}
	if err := a.SetDeviceState(DeviceRoleWeb, DeviceStateInactive, now); err != nil || a.Web.State != DeviceStateInactive {
		t.Fatalf("set state: %v, %+v", err, a.Web)
	}
}
