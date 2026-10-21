package proofingengine

import (
	"context"
	"encoding/base64"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// taggedRegula knows transactions by id with their tag, and records deletes.
type taggedRegula struct {
	tags    map[string]string
	deleted []string
}

func (f *taggedRegula) GetLiveness(_ context.Context, id string) (regula.LivenessTransaction, error) {
	tag, ok := f.tags[id]
	if !ok {
		return regula.LivenessTransaction{}, regula.ErrTransactionNotFound
	}
	return regula.LivenessTransaction{Tag: tag}, nil
}

func (*taggedRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{}, nil
}

func (*taggedRegula) MatchImages(context.Context, string, string) (regula.ImageMatch, error) {
	return regula.ImageMatch{}, nil
}

func (f *taggedRegula) DeleteLiveness(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

// Release deletes only the session's own transaction: another session's,
// sent to this one, is left for that session.
func TestRegulaReleaseOwnOnly(t *testing.T) {
	client := &taggedRegula{tags: map[string]string{"mine": "ips-ref-a", "theirs": "ips-ref-b"}}
	v := regulaFaceVerifier{client: client}
	sess := session.Session{TenantReference: "ref-a"}
	for _, id := range []string{"mine", "theirs", "unknown"} {
		v.Release(context.Background(), sess, liveCapture{TransactionID: id})
	}
	if !slices.Equal(client.deleted, []string{"mine"}) {
		t.Errorf("deleted %v, want only the session's own transaction", client.deleted)
	}
}

// confirmedRegula confirms every tagged transaction live and matches it with
// similarity, returning crop as the live face.
type confirmedRegula struct {
	*taggedRegula
	similarity float64
	crop       string
}

func (f *confirmedRegula) GetLiveness(ctx context.Context, id string) (regula.LivenessTransaction, error) {
	tx, err := f.taggedRegula.GetLiveness(ctx, id)
	tx.Status = ptr(0)
	return tx, err
}

func (f *confirmedRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{Similarity: f.similarity, LiveCrop: f.crop}, nil
}

// A confirmed Regula liveness is accepted and matched against the threshold;
// Regula gives a pass or fail, never a liveness score, and the match's crop
// is kept as the selfie.
func TestRegulaConfirmedLiveness(t *testing.T) {
	const threshold = 0.8
	crop := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nlive-face"))
	ref := &faceReference{ImageBase64: "cG9ydHJhaXQ=", MimeType: "image/jpeg"}
	sess := session.Session{TenantReference: "ref"}
	for _, c := range []struct {
		name       string
		similarity float64
		matched    bool
	}{
		{"at the threshold", threshold, true},
		{"below the threshold", threshold - 0.01, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := &confirmedRegula{taggedRegula: &taggedRegula{tags: map[string]string{"tx": "ips-ref"}}, similarity: c.similarity, crop: crop}
			v := regulaFaceVerifier{client: client, threshold: threshold}
			out, err := v.Verify(context.Background(), sess, ref, liveCapture{TransactionID: "tx"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !out.LivenessPassed || out.LivenessScore != nil {
				t.Errorf("liveness = %v, score %v; want passed without a score", out.LivenessPassed, out.LivenessScore)
			}
			if out.Matched == nil || *out.Matched != c.matched || out.MatchScore == nil || *out.MatchScore != c.similarity {
				t.Errorf("matched = %v, score %v; want %v at %v", out.Matched, out.MatchScore, c.matched, c.similarity)
			}
			if out.Selfie != crop || out.SelfieMime != "image/png" {
				t.Errorf("selfie = %q (%s); want the match's crop", out.Selfie, out.SelfieMime)
			}
		})
	}
}

// A flow's own face match threshold decides the match, lower or higher than the
// deployment's; without one the deployment's applies.
func TestFlowFaceMatchThreshold(t *testing.T) {
	const deployment = 0.75
	crop := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nlive-face"))
	ref := &faceReference{ImageBase64: "cG9ydHJhaXQ=", MimeType: "image/jpeg"}
	sess := session.Session{TenantReference: "ref"}
	for _, c := range []struct {
		name       string
		threshold  map[flow.Check]float64
		similarity float64
		matched    bool
		reported   float64
	}{
		{"no flow threshold, under the deployment's", nil, 0.7, false, deployment},
		{"no flow threshold, at the deployment's", nil, deployment, true, deployment},
		{"flow threshold under the deployment's", map[flow.Check]float64{flow.CheckFaceMatch: 0.6}, 0.7, true, 0.6},
		{"flow threshold over the deployment's", map[flow.Check]float64{flow.CheckFaceMatch: 0.9}, 0.8, false, 0.9},
		{"another check's threshold", map[flow.Check]float64{flow.CheckNFCPassiveAuth: 0.5}, 0.7, false, deployment},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := &confirmedRegula{taggedRegula: &taggedRegula{tags: map[string]string{"tx": "ips-ref"}}, similarity: c.similarity, crop: crop}
			s := &Server{cfg: Config{Regula: client, RegulaFaceMatchThreshold: deployment}}
			v, ok := s.faceVerifier(flow.FaceProviderRegula, &flow.FlowDefinition{CheckThresholds: c.threshold})
			if !ok {
				t.Fatal("faceVerifier: Regula is configured, want a verifier")
			}
			out, err := v.Verify(context.Background(), sess, ref, liveCapture{TransactionID: "tx"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if out.Matched == nil || out.Threshold == nil {
				t.Fatalf("matched = %v, threshold = %v, want both set", out.Matched, out.Threshold)
			}
			if *out.Matched != c.matched {
				t.Errorf("matched = %v at %v, want %v", *out.Matched, c.similarity, c.matched)
			}
			if *out.Threshold != c.reported {
				t.Errorf("threshold = %v, want %v", *out.Threshold, c.reported)
			}
		})
	}
}

// A Substantial flow with no threshold of its own gets the deployment's, and
// Regula's default must not sit under the floor Substantial refuses.
func TestRegulaDefaultMeetsFloor(t *testing.T) {
	if regula.DefaultMatchThreshold < flow.SubstantialMinFaceMatch {
		t.Errorf("regula.DefaultMatchThreshold = %v, under flow.SubstantialMinFaceMatch %v", regula.DefaultMatchThreshold, flow.SubstantialMinFaceMatch)
	}
}
