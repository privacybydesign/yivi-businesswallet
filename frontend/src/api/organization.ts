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

// The caller's own re-identification state in this organisation. It rides on
// the org detail because a plain member cannot read the member list, and it is
// what the in-app banner is driven from.
export const ownIdentityStateSchema = z.object({
  status: z.string(),
  dueAt: z.string().nullable(),
});

export type OwnIdentityState = z.infer<typeof ownIdentityStateSchema>;

export const organizationDetailSchema = organizationSchema.extend({
  role: z.string(),
  // Absent for a platform admin who is not a member of the org.
  identity: ownIdentityStateSchema.optional(),
});

export type OrganizationDetail = z.infer<typeof organizationDetailSchema>;

// Member type and re-identification status, mirrored from the backend
// (internal/organization/organization.go). Status is derived server-side from
// the identity timestamps, never stored, so it is read-only here.
export const MEMBER_TYPES = ["employee", "external"] as const;

export type MemberType = (typeof MEMBER_TYPES)[number];

export const IDENTITY_STATUSES = [
  "never",
  "verified",
  "due_soon",
  "overdue",
  "requested",
] as const;

export type IdentityStatus = (typeof IDENTITY_STATUSES)[number];

// A status the backend adds and this list omits must not fail the whole member
// list, so the schema falls back to the value verbatim and the UI renders it as
// a neutral badge (see identityStatusTone).
const identityStatusSchema = z.string();

const memberIdentityFields = {
  memberType: z.string(),
  externalOrganisation: z.string().nullable(),
  identityStatus: identityStatusSchema,
  identityDueAt: z.string().nullable(),
  identityRequestedAt: z.string().nullable(),
};

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
  identityVerifiedAt: z.string().nullable(),
  identityRequestedBy: z.string().nullable(),
  ...memberIdentityFields,
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
  identityVerifiedAt: z.string().nullable(),
  ...memberIdentityFields,
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
    memberType?: MemberType;
    externalOrganisation?: string;
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

// --- Re-identification lifecycle (#240) ---

// An org's re-identification policy. `configured` is false until an admin saves
// one, in which case the feature is off: no due dates, no reminders.
export const identitySettingsSchema = z.object({
  configured: z.boolean(),
  employeeIntervalMonths: z.number().nullable(),
  externalIntervalMonths: z.number().nullable(),
  reminderDaysBefore: z.array(z.number()),
  overdueReminderIntervalDays: z.number(),
  overdueReminderMaxCount: z.number(),
  credentialMaxAgeDays: z.number().nullable(),
  overdueConsequence: z.string(),
  updatedAt: z.string().optional(),
});

export type IdentitySettings = z.infer<typeof identitySettingsSchema>;

export const OVERDUE_CONSEQUENCES = ["flag", "block"] as const;

export type OverdueConsequence = (typeof OVERDUE_CONSEQUENCES)[number];

export interface IdentitySettingsInput {
  employeeIntervalMonths: number | null;
  externalIntervalMonths: number | null;
  reminderDaysBefore: number[];
  overdueReminderIntervalDays: number;
  overdueReminderMaxCount: number;
  credentialMaxAgeDays: number | null;
  overdueConsequence: OverdueConsequence;
}

export function getIdentitySettings(
  slug: string,
  signal?: AbortSignal,
): Promise<IdentitySettings> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/identity-settings`, {
    schema: identitySettingsSchema,
    signal,
  });
}

export function saveIdentitySettings(
  slug: string,
  input: IdentitySettingsInput,
  signal?: AbortSignal,
): Promise<IdentitySettings> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/identity-settings`, {
    schema: identitySettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

const requestIdentificationResultSchema = z.object({
  requested: z.number(),
});

export type RequestIdentificationResult = z.infer<
  typeof requestIdentificationResultSchema
>;

// requestIdentification asks one or several members to re-confirm their
// identity now: it flips their status to `requested` and mails each of them a
// re-identification link. The bulk route is used for more than one member so a
// selection is one call and one audited action per member.
export function requestIdentification(
  slug: string,
  userIds: string[],
  reason?: string,
  signal?: AbortSignal,
): Promise<RequestIdentificationResult> {
  const base = `/api/v1/orgs/${encodeURIComponent(slug)}/members`;
  const single = userIds.length === 1;
  return request(
    single
      ? `${base}/${encodeURIComponent(userIds[0])}/request-identification`
      : `${base}/request-identification`,
    {
      schema: requestIdentificationResultSchema,
      method: "POST",
      body: single ? { reason } : { userIds, reason },
      signal,
    },
  );
}

export function updateMemberType(
  slug: string,
  userId: string,
  input: { memberType: MemberType; externalOrganisation: string | null },
  signal?: AbortSignal,
): Promise<Member> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}/type`,
    {
      schema: memberSchema,
      method: "PATCH",
      body: input,
      signal,
    },
  );
}

const reidentifyLinkSchema = z.object({ reidentifyUrl: z.string() });

// mintOwnReidentifyLink is the in-app banner's entry point: the caller mints a
// re-identification link for their own membership rather than waiting for the
// e-mail, through the same token mechanism.
export async function mintOwnReidentifyLink(
  slug: string,
  signal?: AbortSignal,
): Promise<string> {
  const { reidentifyUrl } = await request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/me/reidentify-token`,
    { schema: reidentifyLinkSchema, method: "POST", signal },
  );
  return reidentifyUrl;
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
