package proofingengine

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

type recordedDeviceEvent struct {
	tenant, session string
	event           DeviceEvent
	details         map[string]any
}

type fakeDeviceTrail struct{ events []recordedDeviceEvent }

func (f *fakeDeviceTrail) RecordDeviceEvent(_ context.Context, tenantID, sessionID string, event DeviceEvent, details map[string]any) error {
	f.events = append(f.events, recordedDeviceEvent{tenantID, sessionID, event, details})
	return nil
}

// Which device took part, and a handover, reach the org's audit log with
// only the device keys; how a device behaved (a reconnect) does not.
func TestDeviceEventsReachAudit(t *testing.T) {
	trail := &fakeDeviceTrail{}
	s := &Server{cfg: Config{DeviceTrail: trail}}
	sess := session.Session{ID: "ps_1", TenantID: "org"}

	s.recordDeviceEvent(sess, eventDeviceClaimed, map[string]any{"role": "native", "via": "claim", "deviceId": "d1", "document": "not for the log"})
	s.recordDeviceEvent(sess, eventSessionHandedOver, map[string]any{"role": "native", "deviceId": "d2", "previousDeviceId": "d1"})
	s.recordDeviceEvent(sess, eventDeviceReconnected, map[string]any{"role": "native", "deviceId": "d2"})

	if len(trail.events) != 2 {
		t.Fatalf("recorded %d events, want the claim and the handover: %+v", len(trail.events), trail.events)
	}
	claimed := trail.events[0]
	if claimed.event != DeviceClaimed || claimed.tenant != "org" || claimed.session != "ps_1" || claimed.details["deviceId"] != "d1" {
		t.Errorf("claim = %+v", claimed)
	}
	if _, ok := claimed.details["document"]; ok {
		t.Error("a detail outside the device keys reached the audit log")
	}
	if handed := trail.events[1]; handed.event != DeviceHandedOver || handed.details["previousDeviceId"] != "d1" {
		t.Errorf("handover = %+v", handed)
	}
}
