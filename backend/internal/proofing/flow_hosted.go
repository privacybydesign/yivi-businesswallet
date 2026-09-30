package proofing

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// ErrHostedDisabled is a hosted link for a flow whose hosted page is off.
var ErrHostedDisabled = errors.New("proofing: this flow's hosted page is switched off")

// Completion is how a flow's hosted page ends.
type Completion string

const (
	// CompletionRedirect sends the subject to the session's redirect when it
	// has one, else shows the page's own thank-you screen.
	CompletionRedirect Completion = "redirect"
	// CompletionDone always ends on the page's own thank-you screen: a session
	// on the flow may not carry a redirect.
	CompletionDone Completion = "done"
)

// FlowHosted is how a flow's hosted page behaves in an org. Locales empty is
// every supported language.
type FlowHosted struct {
	Enabled    bool
	Locales    []email.Locale
	Completion Completion
}

// DefaultFlowHosted is a flow's hosted page before an admin set it.
func DefaultFlowHosted() FlowHosted {
	return FlowHosted{Enabled: true, Locales: []email.Locale{}, Completion: CompletionRedirect}
}

// Offers reports whether the page may be shown in locale.
func (f FlowHosted) Offers(locale email.Locale) bool {
	return len(f.Locales) == 0 || slices.Contains(f.Locales, locale)
}

func (f FlowHosted) auditFields() map[string]any {
	return map[string]any{"enabled": f.Enabled, "locales": f.Locales, "completion": string(f.Completion)}
}

// FlowHostedStore persists each flow's hosted page settings per org.
type FlowHostedStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewFlowHostedStore(db database.DB, recorder audit.Recorder) *FlowHostedStore {
	return &FlowHostedStore{db: db, audit: recorder}
}

// Get returns the flow's settings; a flow never set reads as DefaultFlowHosted.
func (s *FlowHostedStore) Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowHosted, error) {
	return getFlowHosted(ctx, s.db, orgID, flowID)
}

func getFlowHosted(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (FlowHosted, error) {
	var f FlowHosted
	var completion string
	err := q.QueryRow(ctx, `SELECT enabled, locales, completion FROM identity_proofing_flow_hosted_settings
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&f.Enabled, &f.Locales, &completion)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultFlowHosted(), nil
	}
	if err != nil {
		return FlowHosted{}, fmt.Errorf("proofing: read hosted settings flow %s: %w", flowID, err)
	}
	f.Completion = Completion(completion)
	return f, nil
}

// Save replaces the flow's settings and audits
// identity_proofing.flow_hosted_configured with before and after. Saving what
// it already has changes and audits nothing.
func (s *FlowHostedStore) Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowHosted) (FlowHosted, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowHosted(ctx, q, orgID, flowID)
		if err != nil {
			return err
		}
		if before.Enabled == f.Enabled && before.Completion == f.Completion && slices.Equal(before.Locales, f.Locales) {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_hosted_settings
				(organization_id, flow_id, enabled, locales, completion)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (organization_id, flow_id) DO UPDATE SET
				enabled = EXCLUDED.enabled, locales = EXCLUDED.locales, completion = EXCLUDED.completion,
				updated_at = now()`,
			orgID, flowID, f.Enabled, f.Locales, string(f.Completion)); err != nil {
			return fmt.Errorf("proofing: save hosted settings flow %s: %w", flowID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowHostedConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(before.auditFields(), f.auditFields()))
	})
	if err != nil {
		return FlowHosted{}, err
	}
	return f, nil
}

// flowHosted is the flow's hosted page settings; a service without a store
// has the defaults.
func (s *Service) flowHosted(ctx context.Context, orgID uuid.UUID, flowID string) (FlowHosted, error) {
	if s.flowHostedSettings == nil {
		return DefaultFlowHosted(), nil
	}
	return s.flowHostedSettings.Get(ctx, orgID, flowID)
}

// FlowHosted returns one of the org's flows' hosted page settings.
func (s *Service) FlowHosted(ctx context.Context, org Org, flowID string) (FlowHosted, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return FlowHosted{}, err
	}
	return s.flowHosted(ctx, org.ID, flowID)
}

// SaveFlowHosted replaces one of the org's flows' hosted page settings: the
// languages are supported ones, kept once in the order given.
func (s *Service) SaveFlowHosted(ctx context.Context, org Org, flowID string, f FlowHosted) (FlowHosted, error) {
	if s.flowHostedSettings == nil {
		return FlowHosted{}, errors.New("proofing: no hosted settings store configured")
	}
	if f.Completion != CompletionRedirect && f.Completion != CompletionDone {
		return FlowHosted{}, fmt.Errorf("%w: completion is redirect or done", ErrInvalidInput)
	}
	locales := make([]email.Locale, 0, len(f.Locales))
	for _, raw := range f.Locales {
		locale, ok := email.ParseLocale(string(raw))
		if !ok {
			return FlowHosted{}, fmt.Errorf("%w: unsupported language %q", ErrInvalidInput, raw)
		}
		if !slices.Contains(locales, locale) {
			locales = append(locales, locale)
		}
	}
	f.Locales = locales
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return FlowHosted{}, err
	}
	return s.flowHostedSettings.Save(ctx, org.ID, flowID, f)
}

// orgHasFlow is ErrFlowNotFound for a flow that is not one of the org's.
func (s *Service) orgHasFlow(ctx context.Context, org Org, flowID string) error {
	apiKey, err := s.orgAPIKey(ctx, org)
	if err != nil {
		return err
	}
	flows, err := s.ips.ListFlows(ctx, apiKey)
	if err != nil {
		return fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	if !slices.ContainsFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == flowID }) {
		return ErrFlowNotFound
	}
	return nil
}
