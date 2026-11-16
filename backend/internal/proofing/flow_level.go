package proofing

import (
	"fmt"
	"maps"
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// The levels a flow may name besides those it can require: none requires
// none, and high is one no check here reaches.
const (
	flowLevelNone = "none"
	flowLevelHigh = "high"
)

// substantialFaceMatchFloor is the lowest face match threshold a Substantial
// flow may set (flow.SubstantialMinFaceMatch in the engine).
const substantialFaceMatchFloor = 0.75

// levelNeeds is what a flow requiring a level must hold, as the wallet's flow
// editor ticks it (withAssuranceLevel) and the engine requires it
// (flow.LevelRequirements).
type levelNeeds struct {
	steps        []string
	checks       []string
	faceProvider string
	minFaceMatch float64
}

var flowLevelNeeds = map[string]levelNeeds{
	eidasLow: {steps: []string{stepDocumentCapture, stepNFCRead}, checks: substantialChecks[:1]},
	eidasSubstantial: {
		steps: []string{stepDocumentCapture, stepNFCRead, stepFaceVerification}, checks: substantialChecks,
		faceProvider: faceProviderRegula, minFaceMatch: substantialFaceMatchFloor,
	},
}

// withLevelNeeds adds to in what its required level needs, so a flow never
// claims a level its checks cannot reach; the rest of in stays. none (or no
// level) requires nothing; high is refused.
func withLevelNeeds(in proofingprovider.FlowSpec) (proofingprovider.FlowSpec, error) {
	switch in.RequiredAssuranceLevel {
	case "", flowLevelNone:
		in.RequiredAssuranceLevel = ""
		return in, nil
	case flowLevelHigh:
		return in, fmt.Errorf("%w: no check here reaches high: require none, low or substantial", ErrInvalidInput)
	}
	needs, ok := flowLevelNeeds[in.RequiredAssuranceLevel]
	if !ok {
		return in, fmt.Errorf("%w: the assurance level is none, low or substantial", ErrInvalidInput)
	}
	in.Steps = withAll(in.Steps, needs.steps)
	in.RequiredChecks = withAll(in.RequiredChecks, needs.checks)
	if needs.faceProvider != "" {
		in.FaceProvider = needs.faceProvider
	}
	if threshold, ok := in.CheckThresholds[checkFaceMatch]; ok && threshold < needs.minFaceMatch {
		in.CheckThresholds = maps.Clone(in.CheckThresholds)
		in.CheckThresholds[checkFaceMatch] = needs.minFaceMatch
	}
	// The chip read runs in the Idem app.
	if len(in.Apps) > 0 && !slices.Contains(in.Apps, appIdem) {
		in.Apps = append(slices.Clone(in.Apps), appIdem)
	}
	return in, nil
}

// withAll is have with every one of want it lacks added.
func withAll(have, want []string) []string {
	out := slices.Clone(have)
	for _, v := range want {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
