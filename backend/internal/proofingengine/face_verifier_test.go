package proofingengine

import (
	"context"
	"slices"
	"testing"

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
