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
  namePrefix: z.string().nullable(),
  lastName: z.string(),
  role: z.string(),
  jobTitle: z.string().nullable(),
  departmentId: z.string().nullable(),
  departmentName: z.string().nullable(),
});

export type Member = z.infer<typeof memberSchema>;

const memberListSchema = z.array(memberSchema);

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

export function getOrganizationMembers(
  slug: string,
  signal?: AbortSignal,
): Promise<Member[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/members`, {
    schema: memberListSchema,
    signal,
  });
}

export function inviteMember(
  slug: string,
  input: {
    email: string;
    givenNames: string;
    lastName: string;
    preferredName?: string;
    namePrefix?: string;
    role?: string;
    jobTitle?: string;
    departmentId?: string;
  },
  signal?: AbortSignal,
): Promise<Member> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/members`, {
    schema: memberSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function updateOrganizationMember(
  slug: string,
  userId: string,
  input: { jobTitle: string | null; departmentId: string | null },
  signal?: AbortSignal,
): Promise<Member> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}`,
    {
      schema: memberSchema,
      method: "PATCH",
      body: input,
      signal,
    },
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
): Promise<Department> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/departments`, {
    schema: departmentSchema,
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
): Promise<Department> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/departments/${encodeURIComponent(departmentId)}`,
    {
      schema: departmentSchema,
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
