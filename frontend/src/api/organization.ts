import { z } from "zod";
import { absoluteApiUrl, request } from "./http";

export const departmentSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  name: z.string(),
});

export type Department = z.infer<typeof departmentSchema>;

const departmentListSchema = z.array(departmentSchema);

// An organization is a business wallet: identity from the KVK register plus the
// wallet's QERDS digital address and lifecycle status.
export const organizationSchema = z.object({
  id: z.string(),
  name: z.string(),
  slug: z.string(),
  kvkNumber: z.string(),
  euid: z.string(),
  digitalAddress: z.string(),
  status: z.string(),
  bootstrappedAt: z.string(),
  // What happens to the owner's data when the provider stops serving them
  // (Art 7(6)(f)); captured in advance because termination is exactly the moment
  // nobody can be asked.
  dataInstruction: z.string(),
  terminatedAt: z.string().optional(),
  // Set when a termination honoured a delete instruction: the bundle went out
  // and erasure is owed.
  erasurePendingAt: z.string().optional(),
  // Only the list endpoints carry the org's theme logo path (absent when the org
  // has no logo, or on single-org endpoints), so the org switcher can show it.
  logoUri: z.string().optional(),
});

export type Organization = z.infer<typeof organizationSchema>;

const organizationListSchema = z.array(organizationSchema);

// The backend returns each org's logo as a path on the API; make it absolute so
// an <img> loads it from the API origin even when the SPA is served elsewhere
// (mirrors withAbsoluteLogo in theme.ts).
export function withAbsoluteLogos(orgs: Organization[]): Organization[] {
  return orgs.map((org) =>
    org.logoUri ? { ...org, logoUri: absoluteApiUrl(org.logoUri) } : org,
  );
}

// The avatar path needs the same treatment as a logo: the backend returns an API
// path, and an <img> has to load it from the API origin. Applied to every shape
// that carries one (members, member list entries, audit-log actors).
function withAbsoluteAvatar<T extends { avatarUri: string }>(subject: T): T {
  return subject.avatarUri
    ? { ...subject, avatarUri: absoluteApiUrl(subject.avatarUri) }
    : subject;
}

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
  phone: z.string().nullable(),
  verified: z.boolean(),
  avatarUri: z.string(),
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
  phone: z.string().nullable(),
  verified: z.boolean(),
  avatarUri: z.string(),
});

export type MemberListEntry = z.infer<typeof memberListEntrySchema>;

export const memberListPageSchema = z.object({
  entries: z.array(memberListEntrySchema),
  total: z.number(),
});

export type MemberListPage = z.infer<typeof memberListPageSchema>;

export type MemberSort =
  | "name"
  | "email"
  | "jobtitle"
  | "role"
  | "department"
  | "status";
export type SortDir = "asc" | "desc";

export interface MemberListParams {
  status?: "active" | "invited";
  q?: string;
  sort?: MemberSort;
  dir?: SortDir;
  limit?: number;
  offset?: number;
}

export async function getOrganizations(
  signal?: AbortSignal,
): Promise<Organization[]> {
  const orgs = await request("/api/v1/organizations", {
    schema: organizationListSchema,
    signal,
  });
  return withAbsoluteLogos(orgs);
}

export async function getMyOrganizations(
  signal?: AbortSignal,
): Promise<Organization[]> {
  const orgs = await request("/api/v1/me/organizations", {
    schema: organizationListSchema,
    signal,
  });
  return withAbsoluteLogos(orgs);
}

// deleteOrganization removes an organization by id (platform-admin only). All
// org-scoped data cascades server-side.
export function deleteOrganization(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/organizations/${encodeURIComponent(id)}`, {
    schema: z.void(),
    method: "DELETE",
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

export async function getOrganizationMembers(
  slug: string,
  params: MemberListParams = {},
  signal?: AbortSignal,
): Promise<MemberListPage> {
  const search = new URLSearchParams();
  if (params.status) search.set("status", params.status);
  if (params.q) search.set("q", params.q);
  if (params.sort) search.set("sort", params.sort);
  if (params.dir) search.set("dir", params.dir);
  if (params.limit !== undefined) search.set("limit", String(params.limit));
  if (params.offset !== undefined) search.set("offset", String(params.offset));
  const query = search.toString();
  const page = await request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/members${query ? `?${query}` : ""}`,
    {
      schema: memberListPageSchema,
      signal,
    },
  );
  return { ...page, entries: page.entries.map(withAbsoluteAvatar) };
}

export async function getOrganizationMember(
  slug: string,
  userId: string,
  signal?: AbortSignal,
): Promise<Member> {
  return withAbsoluteAvatar(
    await request(
      `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}`,
      {
        schema: memberSchema,
        signal,
      },
    ),
  );
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

// removeMember off-boards an active member, revoking their membership. Returns
// no body (204); the server refuses to remove the last admin (409).
export function removeMember(
  slug: string,
  userId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}`,
    {
      schema: z.void(),
      method: "DELETE",
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
  avatarUri: z.string(),
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

// An actor's avatar path is org-scoped, like a member's, so it needs the same
// absolutising before it reaches an <img>.
function withAbsoluteActorAvatars(page: AuditEventsPage): AuditEventsPage {
  return {
    ...page,
    events: page.events.map((event) =>
      event.actor
        ? { ...event, actor: withAbsoluteAvatar(event.actor) }
        : event,
    ),
  };
}

export async function getOrganizationAuditEvents(
  slug: string,
  cursor?: string,
  signal?: AbortSignal,
): Promise<AuditEventsPage> {
  const params = new URLSearchParams();
  if (cursor) {
    params.set("cursor", cursor);
  }
  const query = params.toString();
  return withAbsoluteActorAvatars(
    await request(
      `/api/v1/orgs/${encodeURIComponent(slug)}/audit-events${query ? `?${query}` : ""}`,
      {
        schema: auditEventsPageSchema,
        signal,
      },
    ),
  );
}

export async function getMemberAuditEvents(
  slug: string,
  userId: string,
  cursor?: string,
  signal?: AbortSignal,
): Promise<AuditEventsPage> {
  const params = new URLSearchParams();
  if (cursor) {
    params.set("cursor", cursor);
  }
  const query = params.toString();
  return withAbsoluteActorAvatars(
    await request(
      `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}/audit-events${query ? `?${query}` : ""}`,
      {
        schema: auditEventsPageSchema,
        signal,
      },
    ),
  );
}
