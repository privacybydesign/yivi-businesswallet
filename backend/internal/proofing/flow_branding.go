package proofing

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// FlowBranding is a flow's own branding over the org's. Custom false is "same
// as the organisation"; true is "custom for this flow", whose empty fields
// (and no logo) fall back to the org's. The embedded Branding never carries
// HidePoweredBy: that stays the org's.
type FlowBranding struct {
	Custom bool
	Branding
	UpdatedAt time.Time
}

// over is the branding a flow's mail, hosted page and Idem app wear: b's own
// name, colour, contact, privacy link and logo where set, else base's. The
// white-label colours, the font and HidePoweredBy are not a flow's to set, so
// they stay base's.
func (b Branding) over(base Branding) Branding {
	out := base
	out.DisplayName = cmp.Or(b.DisplayName, base.DisplayName)
	out.PrimaryColor = cmp.Or(b.PrimaryColor, base.PrimaryColor)
	out.SupportContact = cmp.Or(b.SupportContact, base.SupportContact)
	out.PrivacyURL = cmp.Or(b.PrivacyURL, base.PrivacyURL)
	out.HasLogo = b.HasLogo || base.HasLogo
	return out
}

// brandedFor is s with the flow's own branding over the org's. UpdatedAt, the
// version of the logo's address, becomes the flow's when its logo shows, so a
// replaced logo is fetched again.
func (s Settings) brandedFor(fb FlowBranding) Settings {
	if !fb.Custom {
		return s
	}
	s.Branding = fb.over(s.Branding)
	if fb.HasLogo {
		s.UpdatedAt = fb.UpdatedAt
	}
	return s
}

func (f FlowBranding) auditFields() map[string]any {
	return map[string]any{
		"custom": f.Custom, "displayName": f.DisplayName, "primaryColor": f.PrimaryColor,
		"supportContact": f.SupportContact, "privacyUrl": f.PrivacyURL, "hasLogo": f.HasLogo,
	}
}

// FlowBrandingStore persists each flow's own branding per org. A flow without
// a row is not custom.
type FlowBrandingStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewFlowBrandingStore(db database.DB, recorder audit.Recorder) *FlowBrandingStore {
	return &FlowBrandingStore{db: db, audit: recorder}
}

// Get returns the flow's branding; a flow never made custom reads as not Custom.
func (s *FlowBrandingStore) Get(ctx context.Context, orgID uuid.UUID, flowID string) (FlowBranding, error) {
	return getFlowBranding(ctx, s.db, orgID, flowID)
}

func getFlowBranding(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (FlowBranding, error) {
	f := FlowBranding{Custom: true}
	err := q.QueryRow(ctx, `SELECT COALESCE(display_name, ''), COALESCE(primary_color, ''),
			COALESCE(support_contact, ''), COALESCE(privacy_url, ''), logo_bytes IS NOT NULL, updated_at
		FROM identity_proofing_flow_branding WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).
		Scan(&f.DisplayName, &f.PrimaryColor, &f.SupportContact, &f.PrivacyURL, &f.HasLogo, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowBranding{}, nil
	}
	if err != nil {
		return FlowBranding{}, fmt.Errorf("proofing: read branding flow %s: %w", flowID, err)
	}
	return f, nil
}

// Save replaces the flow's branding, and its logo as logo says, and audits
// identity_proofing.flow_branding_configured with before and after (whether a
// logo is set, never its bytes). Not Custom removes the row, logo included.
// Saving what it already has changes and audits nothing.
func (s *FlowBrandingStore) Save(ctx context.Context, orgID uuid.UUID, flowID string, f FlowBranding, logo LogoChange) (FlowBranding, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowBranding(ctx, q, orgID, flowID)
		if err != nil {
			return err
		}
		after := FlowBranding{}
		if f.Custom {
			after = FlowBranding{Custom: true, Branding: f.Branding}
			after.HasLogo = before.HasLogo
			if logo.Replace {
				after.HasLogo = len(logo.Logo.Bytes) > 0
			}
		}

		// A new logo is a change even when one was set before: only its bytes
		// differ, which the branding does not hold.
		replaced := f.Custom && logo.Replace && len(logo.Logo.Bytes) > 0
		if after.Custom == before.Custom && after.Branding == before.Branding && !replaced {
			return nil
		}
		if err := writeFlowBranding(ctx, q, orgID, flowID, after, logo); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowBrandingConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(before.auditFields(), after.auditFields()))
	})
	if err != nil {
		return FlowBranding{}, err
	}
	return s.Get(ctx, orgID, flowID)
}

func writeFlowBranding(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string, f FlowBranding, logo LogoChange) error {
	if !f.Custom {
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_flow_branding
			WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID); err != nil {
			return fmt.Errorf("proofing: remove branding flow %s: %w", flowID, err)
		}
		return nil
	}
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_branding
			(organization_id, flow_id, display_name, primary_color, support_contact, privacy_url,
			 logo_bytes, logo_content_type)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
			CASE WHEN $7::boolean THEN $8::bytea END, CASE WHEN $7 THEN NULLIF($9::text, '') END)
		ON CONFLICT (organization_id, flow_id) DO UPDATE SET
			display_name = EXCLUDED.display_name, primary_color = EXCLUDED.primary_color,
			support_contact = EXCLUDED.support_contact, privacy_url = EXCLUDED.privacy_url,
			logo_bytes = CASE WHEN $7 THEN EXCLUDED.logo_bytes ELSE identity_proofing_flow_branding.logo_bytes END,
			logo_content_type = CASE WHEN $7 THEN EXCLUDED.logo_content_type
				ELSE identity_proofing_flow_branding.logo_content_type END,
			updated_at = now()`,
		orgID, flowID, f.DisplayName, f.PrimaryColor, f.SupportContact, f.PrivacyURL,
		logo.Replace, nilIfEmpty(logo.Logo.Bytes), logo.Logo.ContentType); err != nil {
		return fmt.Errorf("proofing: save branding flow %s: %w", flowID, err)
	}
	return nil
}

// Logo returns the flow's own logo, or ErrNoLogo.
func (s *FlowBrandingStore) Logo(ctx context.Context, orgID uuid.UUID, flowID string) (Logo, error) {
	var logo Logo
	var contentType *string
	err := s.db.QueryRow(ctx, `SELECT logo_bytes, logo_content_type FROM identity_proofing_flow_branding
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&logo.Bytes, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Logo{}, ErrNoLogo
	}
	if err != nil {
		return Logo{}, fmt.Errorf("proofing: read logo flow %s: %w", flowID, err)
	}
	if contentType == nil {
		return Logo{}, ErrNoLogo
	}
	logo.ContentType = *contentType
	return logo, nil
}

// flowBranding is the flow's own branding; a service without a store has
// none.
func (s *Service) flowBranding(ctx context.Context, orgID uuid.UUID, flowID string) (FlowBranding, error) {
	if s.flowBrandings == nil {
		return FlowBranding{}, nil
	}
	return s.flowBrandings.Get(ctx, orgID, flowID)
}

// FlowBranding returns one of the org's flows' own branding.
func (s *Service) FlowBranding(ctx context.Context, org Org, flowID string) (FlowBranding, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return FlowBranding{}, err
	}
	return s.flowBranding(ctx, org.ID, flowID)
}

// SaveFlowBranding replaces one of the org's flows' own branding, checked as
// the org's is. A save that is not Custom removes it, so its fields go
// unchecked.
func (s *Service) SaveFlowBranding(ctx context.Context, org Org, flowID string, f FlowBranding, logo LogoChange) (FlowBranding, error) {
	if s.flowBrandings == nil {
		return FlowBranding{}, errors.New("proofing: no flow branding store configured")
	}
	if f.Custom {
		b, err := checkBranding(f.Branding)
		if err != nil {
			return FlowBranding{}, err
		}
		f.Branding = b
	}
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return FlowBranding{}, err
	}
	return s.flowBrandings.Save(ctx, org.ID, flowID, f, logo)
}

// FlowBrandingLogo returns the flow's own logo, or ErrNoLogo.
func (s *Service) FlowBrandingLogo(ctx context.Context, org Org, flowID string) (Logo, error) {
	if s.flowBrandings == nil {
		return Logo{}, ErrNoLogo
	}
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return Logo{}, err
	}
	return s.flowBrandings.Logo(ctx, org.ID, flowID)
}

// brandLogo is the logo a flow's branding shows: its own, else the org's.
func (s *Service) brandLogo(ctx context.Context, orgID uuid.UUID, flowID string, fb FlowBranding) (Logo, error) {
	return flowOrOrgLogo(ctx, s.flowBrandings, s.orgSettings, orgID, flowID, fb)
}

type flowLogoReader interface {
	Logo(ctx context.Context, orgID uuid.UUID, flowID string) (Logo, error)
}

type orgLogoReader interface {
	Logo(ctx context.Context, orgID uuid.UUID) (Logo, error)
}

// flowOrOrgLogo is the logo fb shows: the flow's own when it is custom and has
// one, else the org's proofing logo.
func flowOrOrgLogo(ctx context.Context, flows flowLogoReader, org orgLogoReader, orgID uuid.UUID, flowID string, fb FlowBranding) (Logo, error) {
	if fb.Custom && fb.HasLogo {
		return flows.Logo(ctx, orgID, flowID)
	}
	return org.Logo(ctx, orgID)
}
