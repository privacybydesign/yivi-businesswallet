package export

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// memberPageSize is how many member entries are read at a time. The store's
// list is offset-paginated with no unbounded mode, so the section pages to the
// end rather than exporting a first page and calling it the directory.
const memberPageSize = 200

type organizationReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
	ListDepartments(ctx context.Context, orgID uuid.UUID) ([]organization.Department, error)
	ListMemberEntries(ctx context.Context, orgID uuid.UUID, p organization.MemberListParams) ([]organization.MemberEntry, int, error)
}

// OwnerIdentificationWriter fills the owner-identification section: the
// organization's identity root, its departments, and everyone in it.
type OwnerIdentificationWriter struct {
	orgs organizationReader
}

func NewOwnerIdentificationWriter(orgs organizationReader) *OwnerIdentificationWriter {
	return &OwnerIdentificationWriter{orgs: orgs}
}

func (w *OwnerIdentificationWriter) Key() string { return SectionOwnerIdentification }

func (w *OwnerIdentificationWriter) Write(ctx context.Context, orgID uuid.UUID, s *SectionBundle) error {
	org, err := w.orgs.GetByID(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading organization: %w", err)
	}
	if err := s.AddJSON("organization.json", orgProfileOf(org)); err != nil {
		return err
	}

	departments, err := w.orgs.ListDepartments(ctx, orgID)
	if err != nil {
		return fmt.Errorf("export: reading departments: %w", err)
	}
	records := make([]departmentRecord, 0, len(departments))
	for _, d := range departments {
		records = append(records, departmentRecord{ID: d.ID, Name: d.Name})
	}
	s.Count("departments", len(records))
	if err := s.AddJSON("departments.json", records); err != nil {
		return err
	}

	members, err := w.readMembers(ctx, orgID)
	if err != nil {
		return err
	}
	s.Count("members", len(members))
	return s.AddJSON("members.json", members)
}

func (w *OwnerIdentificationWriter) readMembers(ctx context.Context, orgID uuid.UUID) ([]memberRecord, error) {
	records := []memberRecord{}
	for offset := 0; ; offset += memberPageSize {
		entries, total, err := w.orgs.ListMemberEntries(ctx, orgID, organization.MemberListParams{
			Limit:  memberPageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("export: reading members: %w", err)
		}
		for _, e := range entries {
			records = append(records, memberRecordOf(e))
		}
		if len(entries) == 0 || len(records) >= total {
			return records, nil
		}
	}
}

// orgProfile is the organization's identity root (Art 8). LogoURI is left out:
// it is an API path into this deployment, meaningless to a receiver.
type orgProfile struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	KVKNumber      string    `json:"kvkNumber"`
	EUID           string    `json:"euid"`
	DigitalAddress string    `json:"digitalAddress"`
	Status         string    `json:"status"`
	BootstrappedAt string    `json:"bootstrappedAt"`
}

func orgProfileOf(org organization.Organization) orgProfile {
	return orgProfile{
		ID:             org.ID,
		Name:           org.Name,
		Slug:           org.Slug,
		KVKNumber:      org.KVKNumber,
		EUID:           org.EUID,
		DigitalAddress: org.DigitalAddress,
		Status:         org.Status,
		BootstrappedAt: timestamp(org.BootstrappedAt),
	}
}

type departmentRecord struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// memberRecord is one person in the organization, active or invited. The
// invitation's own id and token stay out: the token is a bearer credential and
// the id addresses an in-flight handshake in this deployment, not portable data.
type memberRecord struct {
	Status         string     `json:"status"`
	UserID         *uuid.UUID `json:"userId"`
	Email          string     `json:"email"`
	PreferredName  *string    `json:"preferredName"`
	GivenNames     string     `json:"givenNames"`
	LastName       string     `json:"lastName"`
	Role           string     `json:"role"`
	JobTitle       *string    `json:"jobTitle"`
	DepartmentID   *uuid.UUID `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
	Phone          *string    `json:"phone"`
	Verified       bool       `json:"verified"`
	ExpiresAt      *string    `json:"expiresAt"`
	InvitedBy      *uuid.UUID `json:"invitedBy"`
}

func memberRecordOf(e organization.MemberEntry) memberRecord {
	record := memberRecord{
		Status:         e.Status,
		UserID:         e.UserID,
		Email:          e.Email,
		PreferredName:  e.PreferredName,
		GivenNames:     e.GivenNames,
		LastName:       e.LastName,
		Role:           e.Role,
		JobTitle:       e.JobTitle,
		DepartmentID:   e.DepartmentID,
		DepartmentName: e.DepartmentName,
		Phone:          e.Phone,
		Verified:       e.Verified,
		InvitedBy:      e.InvitedBy,
	}
	if e.ExpiresAt != nil {
		expires := timestamp(*e.ExpiresAt)
		record.ExpiresAt = &expires
	}
	return record
}
