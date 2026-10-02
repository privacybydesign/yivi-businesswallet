package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
)

func ptr[T any](v T) *T { return &v }

// verifiedChip is a chip read whose Passive and Active Authentication verified.
func verifiedChip() *chipChecksInfo {
	return &chipChecksInfo{
		PassiveAuthentication: &passiveAuthInfo{
			SODSignatureValid: ptr(true), DataGroupHashesValid: ptr(true), CSCATrustChainValid: ptr(true),
		},
		ActiveAuthentication: &activeAuthInfo{Attempted: ptr(true), Passed: ptr(true)},
	}
}

// liveRegulaMatch is a live face Regula matched against the chip's portrait.
func liveRegulaMatch() appResultRequest {
	return appResultRequest{Biometrics: &biometricsInfo{
		FaceVerified: ptr(true), LivenessResult: "passed", Engine: faceProviderRegula,
	}}
}

func TestComputeEIDASAssuranceLevel(t *testing.T) {
	required := func(l flow.AssuranceLevel) *flow.FlowDefinition {
		return &flow.FlowDefinition{RequiredAssuranceLevel: l}
	}
	noAA := verifiedChip()
	noAA.ActiveAuthentication = nil
	untrusted := verifiedChip()
	untrusted.PassiveAuthentication.CSCATrustChainValid = ptr(false)
	notLive := liveRegulaMatch()
	notLive.Biometrics.LivenessResult = "failed"
	onDevice := liveRegulaMatch()
	onDevice.Biometrics.Engine, onDevice.Biometrics.LivenessScore = "on_device", ptr(0.9)

	for _, tc := range []struct {
		name          string
		fd            *flow.FlowDefinition
		req           appResultRequest
		checks        *chipChecksInfo
		chipReference bool
		want          flow.AssuranceLevel
	}{
		{"no required level claims none", required(""), liveRegulaMatch(), verifiedChip(), true, ""},
		{"no flow claims none", nil, liveRegulaMatch(), verifiedChip(), true, ""},
		{"everything verified", required(flow.AssuranceLevelLow), liveRegulaMatch(), verifiedChip(), true, flow.AssuranceLevelSubstantial},
		{"chip read alone", required(flow.AssuranceLevelLow), appResultRequest{}, verifiedChip(), true, flow.AssuranceLevelLow},
		{"face without a chip read", required(flow.AssuranceLevelLow), liveRegulaMatch(), nil, true, ""},
		{"untrusted CSCA", required(flow.AssuranceLevelLow), liveRegulaMatch(), untrusted, true, ""},
		{"chip without Active Authentication", required(flow.AssuranceLevelSubstantial), liveRegulaMatch(), noAA, true, flow.AssuranceLevelLow},
		{"liveness failed", required(flow.AssuranceLevelSubstantial), notLive, verifiedChip(), true, flow.AssuranceLevelLow},
		{"face not by Regula", required(flow.AssuranceLevelSubstantial), onDevice, verifiedChip(), true, flow.AssuranceLevelLow},
		{"face not against the chip", required(flow.AssuranceLevelSubstantial), liveRegulaMatch(), verifiedChip(), false, flow.AssuranceLevelLow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeEIDASAssuranceLevel(tc.fd, tc.req, tc.checks, tc.chipReference); got != tc.want {
				t.Errorf("computeEIDASAssuranceLevel = %q, want %q", got, tc.want)
			}
		})
	}
}
