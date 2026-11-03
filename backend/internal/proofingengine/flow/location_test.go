package flow

import "testing"

// A flow is browser-only when no step runs in an app: the document photo is
// the browser's, the face step goes where SelfieLocation says, and the
// document scan and chip read are always the app's.
func TestBrowserOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		fd   FlowDefinition
		want bool
	}{
		"document photo":           {FlowDefinition{Steps: []Step{StepDocumentPhoto}}, true},
		"chip and photo":           {FlowDefinition{Steps: []Step{StepDocumentCapture, StepNFCRead, StepDocumentPhoto}}, false},
		"face in the browser":      {FlowDefinition{Steps: []Step{StepFaceVerification}}, true},
		"face in the app":          {FlowDefinition{Steps: []Step{StepDocumentPhoto, StepFaceVerification}, SelfieLocation: LocationNative}, false},
		"no steps, nothing to run": {FlowDefinition{}, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.fd.BrowserOnly(); got != tc.want {
				t.Errorf("BrowserOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}
