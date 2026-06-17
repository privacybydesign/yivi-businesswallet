import { z } from "zod";
import { request } from "./http";

export const healthResponseSchema = z.object({
  status: z.string(),
});

export type HealthResponse = z.infer<typeof healthResponseSchema>;

export function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return request("/healthz", { schema: healthResponseSchema, signal });
}
