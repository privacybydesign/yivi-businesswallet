import { z } from "zod";
import { request } from "./http";

export const organizationSchema = z.object({
  id: z.string(),
  name: z.string(),
});

export type Organization = z.infer<typeof organizationSchema>;

const organizationListSchema = z.array(organizationSchema);

export function getOrganizations(
  signal?: AbortSignal,
): Promise<Organization[]> {
  return request("/api/v1/organizations", {
    schema: organizationListSchema,
    signal,
  });
}
