package proofingengine

import (
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// The chip access key is handed out only while the chip read is the current
// step: a device that takes a session over after the chip was read (a
// handover) never gets the key to the document.
func TestChipAccessWhileReading(t *testing.T) {
	fd := &flow.FlowDefinition{Steps: []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead, flow.StepFaceVerification}}
	key := &session.ChipAccessKey{DocumentType: "P", DocumentNumber: "SPECI2014"}
	scanned := session.Session{Status: session.StatusInProgress}
	scanned.Steps.Document = &session.DocumentStepEvidence{ChipAccess: key}

	if got := chipAccessFor(scanned, currentStep(scanned, fd)); got != key {
		t.Errorf("chip read pending: chipAccessFor = %v, want the key", got)
	}

	read := scanned
	read.Steps.NFC = &session.NFCStepEvidence{Raw: map[string]any{}}
	if got := chipAccessFor(read, currentStep(read, fd)); got != nil {
		t.Errorf("chip already read: chipAccessFor = %v, want none", got)
	}

	if got := chipAccessFor(scanned, nil); got != nil {
		t.Errorf("no flow: chipAccessFor = %v, want none", got)
	}
}
