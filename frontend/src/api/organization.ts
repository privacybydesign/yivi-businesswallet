import { z } from "zod";
import { request } from "./http";

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
