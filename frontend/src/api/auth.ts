import { z } from "zod";
import { request } from "./http";

export const meSchema = z.object({
  id: z.string(),
  email: z.string(),
});

export type Me = z.infer<typeof meSchema>;

export function claimAuthSession(
  token: string,
  signal?: AbortSignal,
): Promise<Me> {
  return request(`/api/v1/auth/session/${encodeURIComponent(token)}/claim`, {
    schema: meSchema,
    method: "POST",
    signal,
  });
}

export function getMe(signal?: AbortSignal): Promise<Me> {
  return request("/api/v1/me", {
    schema: meSchema,
    signal,
  });
}

export function logout(signal?: AbortSignal): Promise<void> {
  return request("/api/v1/auth/logout", {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
