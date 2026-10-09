package proofing

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// faceOnlyFlow matches the face without reading the chip: each session
// carries the customer's own photo of the person.
var faceOnlyFlow = withFaceProvider(testFlow("f-face", "Face only", []string{"face_verification"}, selfieLocationNative), faceProviderRegula)

// jpegPhoto is a reference photo as a customer sends it: base64 of bytes that
// sniff as a JPEG.
var jpegPhoto = base64.StdEncoding.EncodeToString(append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte("a face")...))

// withFaceOnlyFlow adds faceOnlyFlow to the org and assigns it to initech.
func (f fixture) withFaceOnlyFlow() Customer {
	f.ips.flows = append(f.ips.flows, faceOnlyFlow)
	c := f.customers.byID[initech.ID]
	c.Flows = FlowSelection{FlowIDs: append(c.Flows.FlowIDs, faceOnlyFlow.ID), DefaultFlowID: c.Flows.DefaultFlowID}
	f.customers.byID[initech.ID] = c
	return c
}

func (f fixture) sendWithPhoto(flowID, photo string, channel Channel) (Sent, error) {
	return f.svc.CreateRequest(context.Background(), testOrg, Requester{Name: "Portal key", APIKeyID: new(uuid.UUID)},
		NewRequest{CustomerID: &initech.ID, SubjectEmail: "dibran@example.org", FlowID: flowID, ReferencePhoto: photo, Channel: channel})
}

func TestNeedsReferencePhoto(t *testing.T) {
	for name, tc := range map[string]struct {
		flow       proofingprovider.Flow
		needs      bool
		completes  bool
		yiviOffers bool
	}{
		"face only":      {faceOnlyFlow, true, true, false},
		"chip and face":  {appFlow, false, true, true},
		"chip only":      {chipFlow, false, true, true},
		"browser selfie": {browserFlow, false, false, true},
	} {
		if got := flowNeedsReferencePhoto(tc.flow); got != tc.needs {
			t.Errorf("%s: flowNeedsReferencePhoto = %v, want %v", name, got, tc.needs)
		}
		if got := customerCompletable(tc.flow); got != tc.completes {
			t.Errorf("%s: customerCompletable = %v, want %v", name, got, tc.completes)
		}
		if got := yiviAppAvailable(tc.flow); got != tc.yiviOffers {
			t.Errorf("%s: yiviAppAvailable = %v, want %v", name, got, tc.yiviOffers)
		}
	}
	if flowCompletable(faceOnlyFlow) {
		t.Error("a face-only flow is completable for a member, who has no photo to send")
	}
}

// The photo goes to the engine's session, which matches the live face
// against it; it is never stored on a request that starts its session at once.
func TestRefPhotoReachesSession(t *testing.T) {
	f := newFixture()
	f.withFaceOnlyFlow()
	sent, err := f.sendWithPhoto(faceOnlyFlow.ID, " "+jpegPhoto+"\n", ChannelEmail)
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	got := f.ips.sessions[0].ReferencePhoto
	if got == nil || got.MimeType != "image/jpeg" || got.Base64 != jpegPhoto {
		t.Errorf("session photo = %+v, want the JPEG as sent", got)
	}
	if f.requests.created[0].ReferencePhoto != nil {
		t.Error("a request with its session started stores the photo")
	}
	if sent.DeepLink == "" {
		t.Error("no deep link for the Idem app")
	}
}

func TestReferencePhotoIsChecked(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, maxReferencePhotoBytes)...))
	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	for name, tc := range map[string]struct {
		flowID, photo string
		want          error
	}{
		"missing":        {faceOnlyFlow.ID, "", ErrReferencePhotoRequired},
		"on a chip flow": {chipFlow.ID, jpegPhoto, ErrInvalidInput},
		"not base64":     {faceOnlyFlow.ID, "not base64!", ErrInvalidInput},
		"not an image":   {faceOnlyFlow.ID, svg, ErrInvalidInput},
		"too large":      {faceOnlyFlow.ID, big, ErrInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			f.withFaceOnlyFlow()
			if _, err := f.sendWithPhoto(tc.flowID, tc.photo, ChannelEmail); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if len(f.ips.sessions) != 0 || len(f.requests.created) != 0 {
				t.Error("a refused request started a session or was stored")
			}
		})
	}
}

// A member has no photo to send: a face-only flow is never one for members.
func TestMembersCannotUseFaceOnly(t *testing.T) {
	f := newFixture()
	f.ips.flows = append(f.ips.flows, faceOnlyFlow)
	err := f.svc.ConfigureFlows(context.Background(), testOrg, FlowSelection{FlowIDs: []string{faceOnlyFlow.ID}, DefaultFlowID: faceOnlyFlow.ID})
	if !errors.Is(err, ErrFlowNotCompletable) {
		t.Errorf("ConfigureFlows = %v, want ErrFlowNotCompletable", err)
	}
	if _, err := f.svc.AssignCustomerFlows(context.Background(), testOrg, initech.ID,
		FlowSelection{FlowIDs: []string{faceOnlyFlow.ID}, DefaultFlowID: faceOnlyFlow.ID}); err != nil {
		t.Errorf("AssignCustomerFlows = %v, want a customer may be assigned it", err)
	}
}

// A hosted link holds the photo until its subject starts, then hands it to
// the session.
func TestHostedLinkCarriesPhoto(t *testing.T) {
	f := newFixture()
	f.withFaceOnlyFlow()
	f.svc.SetHostedBaseURL(testHostedBase)
	sent, err := f.sendWithPhoto(faceOnlyFlow.ID, jpegPhoto, ChannelHosted)
	if err != nil {
		t.Fatalf("CreateRequest(hosted): %v", err)
	}
	if held := f.requests.created[0].ReferencePhoto; held == nil || held.Base64 != jpegPhoto {
		t.Fatalf("held photo = %+v, want the JPEG", held)
	}
	if len(f.ips.sessions) != 0 {
		t.Fatal("a hosted link started a session at send")
	}
	token, _ := strings.CutPrefix(sent.HostedURL, testHostedBase)
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodYivi); err == nil {
		t.Error("a face-only link started in the Yivi app")
	}
	if _, err := f.svc.StartHosted(context.Background(), token, proofingprovider.MethodIdem); err != nil {
		t.Fatalf("StartHosted: %v", err)
	}
	if got := f.ips.sessions[0].ReferencePhoto; got == nil || got.Base64 != jpegPhoto {
		t.Errorf("session photo = %+v, want the held JPEG", got)
	}
}
