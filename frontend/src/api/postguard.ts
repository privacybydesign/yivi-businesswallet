import { z } from "zod";
import { request } from "./http";

export const postguardApiKeyInfoSchema = z.object({
  configured: z.boolean(),
  last4: z.string().optional(),
  updatedAt: z.string().optional(),
});

export const postguardEncryptionKeyInfoSchema = z.object({
  configured: z.boolean(),
  updatedAt: z.string().optional(),
});

export const postguardNotificationDeliverySchema = z.enum([
  "postguard",
  "smtp",
]);

export type PostguardNotificationDelivery = z.infer<
  typeof postguardNotificationDeliverySchema
>;

export const postguardSettingsSchema = z.object({
  apiKey: postguardApiKeyInfoSchema,
  encryptionKey: postguardEncryptionKeyInfoSchema,
  notifications: postguardNotificationDeliverySchema,
});

export type PostguardSettings = z.infer<typeof postguardSettingsSchema>;

export const postguardSentFileSchema = z.object({
  id: z.string(),
  fileName: z.string(),
  sizeBytes: z.number(),
  recipients: z.array(z.string()),
  cryptifyUuid: z.string(),
  expiresAfter: z.string().optional(),
  status: z.string(),
  createdAt: z.string(),
});

export type PostguardSentFile = z.infer<typeof postguardSentFileSchema>;

const postguardSentFileListSchema = z.array(postguardSentFileSchema);

export function getPostguardSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<PostguardSettings> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/postguard/settings`,
    { schema: postguardSettingsSchema, signal },
  );
}

export function setPostguardEncryptionKey(
  slug: string,
  input: { key: string },
  signal?: AbortSignal,
): Promise<PostguardSettings> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/postguard/encryption-key`,
    { schema: postguardSettingsSchema, method: "PUT", body: input, signal },
  );
}

export function deletePostguardEncryptionKey(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/postguard/encryption-key`,
    { schema: z.void(), method: "DELETE", signal },
  );
}

export function setPostguardApiKey(
  slug: string,
  input: { apiKey: string },
  signal?: AbortSignal,
): Promise<PostguardSettings> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/postguard/api-key`, {
    schema: postguardSettingsSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function deletePostguardApiKey(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/postguard/api-key`, {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function setPostguardNotifications(
  slug: string,
  input: { notifications: PostguardNotificationDelivery },
  signal?: AbortSignal,
): Promise<PostguardSettings> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/postguard/notifications`,
    { schema: postguardSettingsSchema, method: "PUT", body: input, signal },
  );
}

export function getPostguardFiles(
  slug: string,
  signal?: AbortSignal,
): Promise<PostguardSentFile[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/postguard/files`, {
    schema: postguardSentFileListSchema,
    signal,
  });
}

export interface SendPostguardFileInput {
  files: File[];
  recipients: string[];
  notify: boolean;
  message?: string;
  expiresAfter?: string;
}

export function sendPostguardFile(
  slug: string,
  input: SendPostguardFileInput,
  signal?: AbortSignal,
): Promise<PostguardSentFile> {
  const form = new FormData();
  for (const file of input.files) {
    form.append("file", file);
  }
  for (const recipient of input.recipients) {
    form.append("recipients", recipient);
  }
  form.append("notify", String(input.notify));
  if (input.message) {
    form.append("message", input.message);
  }
  if (input.expiresAfter) {
    form.append("expiresAfter", input.expiresAfter);
  }
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/postguard/files`, {
    schema: postguardSentFileSchema,
    method: "POST",
    body: form,
    signal,
    // Encrypt + upload can take a while for larger files.
    timeoutMs: UPLOAD_TIMEOUT_MS,
  });
}

const UPLOAD_TIMEOUT_MS = 120_000;
