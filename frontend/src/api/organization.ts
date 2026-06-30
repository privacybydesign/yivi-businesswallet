import { z } from "zod";
import { request } from "./http";

export const departmentSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  name: z.string(),
});

export type Department = z.infer<typeof departmentSchema>;

const departmentListSchema = z.array(departmentSchema);

export const organizationSchema = z.object({
  id: z.string(),
  name: z.string(),
  slug: z.string(),
});

export type Organization = z.infer<typeof organizationSchema>;

const organizationListSchema = z.array(organizationSchema);

export const organizationDetailSchema = organizationSchema.extend({
  role: z.string(),
});

export type OrganizationDetail = z.infer<typeof organizationDetailSchema>;

export const memberSchema = z.object({
  userId: z.string(),
  email: z.string(),
  preferredName: z.string().nullable(),
  givenNames: z.string(),
  lastName: z.string(),
  role: z.string(),
  jobTitle: z.string().nullable(),
  departmentId: z.string().nullable(),
  departmentName: z.string().nullable(),
});

export type Member = z.infer<typeof memberSchema>;

// Active member or pending invitation, discriminated by `status`.
export const memberListEntrySchema = z.object({
  status: z.enum(["active", "invited"]),
  userId: z.string().nullable(),
  invitationId: z.string().nullable(),
  email: z.string(),
  preferredName: z.string().nullable(),
  givenNames: z.string(),
  lastName: z.string(),
  role: z.string(),
  jobTitle: z.string().nullable(),
  departmentId: z.string().nullable(),
  departmentName: z.string().nullable(),
  expiresAt: z.string().nullable(),
  invitedBy: z.string().nullable(),
});

export type MemberListEntry = z.infer<typeof memberListEntrySchema>;

const memberListSchema = z.array(memberListEntrySchema);

export function getOrganizations(
  signal?: AbortSignal,
): Promise<Organization[]> {
  return request("/api/v1/organizations", {
    schema: organizationListSchema,
    signal,
  });
}

export function getMyOrganizations(
  signal?: AbortSignal,
): Promise<Organization[]> {
  return request("/api/v1/me/organizations", {
    schema: organizationListSchema,
    signal,
  });
}

export function createOrganization(
  input: { name: string; slug: string },
  signal?: AbortSignal,
): Promise<Organization> {
  return request("/api/v1/organizations", {
    schema: organizationSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function getOrganization(
  slug: string,
  signal?: AbortSignal,
): Promise<OrganizationDetail> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}`, {
    schema: organizationDetailSchema,
    signal,
  });
}

export function updateOrganization(
  slug: string,
  input: { name: string },
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}`, {
    schema: z.void(),
    method: "PATCH",
    body: input,
    signal,
  });
}

export function getOrganizationMembers(
  slug: string,
  signal?: AbortSignal,
): Promise<MemberListEntry[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/members`, {
    schema: memberListSchema,
    signal,
  });
}

// Inviting creates a pending invitation server-side and returns no body (201),
// so there is nothing for the caller to consume — success is the 2xx itself.
export function inviteMember(
  slug: string,
  input: {
    email: string;
    givenNames: string;
    lastName: string;
    role?: string;
    jobTitle?: string;
    departmentId?: string;
  },
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/members`, {
    schema: z.void(),
    method: "POST",
    body: input,
    signal,
  });
}

export function updateOrganizationMember(
  slug: string,
  userId: string,
  input: { role: string; jobTitle: string | null; departmentId: string | null },
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}`,
    {
      schema: z.void(),
      method: "PATCH",
      body: input,
      signal,
    },
  );
}

export function resendInvitation(
  slug: string,
  invitationId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/invitations/${encodeURIComponent(invitationId)}/resend`,
    { schema: z.void(), method: "POST", signal },
  );
}

export function revokeInvitation(
  slug: string,
  invitationId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/invitations/${encodeURIComponent(invitationId)}`,
    { schema: z.void(), method: "DELETE", signal },
  );
}

export function getOrganizationDepartments(
  slug: string,
  signal?: AbortSignal,
): Promise<Department[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/departments`, {
    schema: departmentListSchema,
    signal,
  });
}

export function createDepartment(
  slug: string,
  input: { name: string },
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/departments`, {
    schema: z.void(),
    method: "POST",
    body: input,
    signal,
  });
}

export function updateDepartment(
  slug: string,
  departmentId: string,
  input: { name: string },
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/departments/${encodeURIComponent(departmentId)}`,
    {
      schema: z.void(),
      method: "PATCH",
      body: input,
      signal,
    },
  );
}

export function deleteDepartment(
  slug: string,
  departmentId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/departments/${encodeURIComponent(departmentId)}`,
    {
      schema: z.void(),
      method: "DELETE",
      signal,
    },
  );
}

export const auditActorSchema = z.object({
  userId: z.string(),
  preferredName: z.string().nullable(),
  givenNames: z.string(),
  lastName: z.string(),
});

export type AuditActor = z.infer<typeof auditActorSchema>;

export const auditEventSchema = z.object({
  id: z.string(),
  occurredAt: z.string(),
  action: z.string(),
  targetType: z.string(),
  targetId: z.string(),
  metadata: z.record(z.string(), z.unknown()),
  actor: auditActorSchema.nullable(),
});

export type AuditEvent = z.infer<typeof auditEventSchema>;

export const auditEventsPageSchema = z.object({
  events: z.array(auditEventSchema),
  nextCursor: z.string().nullable(),
});

export type AuditEventsPage = z.infer<typeof auditEventsPageSchema>;

export function getOrganizationAuditEvents(
  slug: string,
  cursor?: string,
  signal?: AbortSignal,
): Promise<AuditEventsPage> {
  const params = new URLSearchParams();
  if (cursor) {
    params.set("cursor", cursor);
  }
  const query = params.toString();
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/audit-events${query ? `?${query}` : ""}`,
    {
      schema: auditEventsPageSchema,
      signal,
    },
  );
}
