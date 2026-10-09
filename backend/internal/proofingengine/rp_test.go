package proofingengine

import (
	"context"
	"errors"
	"net/http"
	"testing"

	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestRetentionOverflowRefused(t *testing.T) {
	s := &Server{}
	spec := pp.FlowSpec{Name: "x", Steps: []string{"document_capture"}, RetentionOverrideSeconds: 1 << 62}
	var refused *pp.RejectedError
	if _, err := s.CreateFlow(context.Background(), pp.Tenant{ID: "org"}, spec); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("CreateFlow(retention 1<<62 s) = %v, want 400", err)
	}
}
