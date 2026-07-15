import { z } from "zod";
import { request, requestBlob } from "./http";

export const qerdsMessageSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  direction: z.enum(["outbound", "inbound"]),
  senderAddress: z.string(),
  recipientAddress: z.string(),
  subject: z.string(),
  body: z.string(),
  providerRef: z.string().optional(),
  status: z.string(),
  submittedAt: z.string().optional(),
  deliveredAt: z.string().optional(),
  qualifiedTimestampSend: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type QerdsMessage = z.infer<typeof qerdsMessageSchema>;

const qerdsMessageListSchema = z.array(qerdsMessageSchema);

// raw is the base64-encoded ERDS evidence blob returned by the provider.
export const qerdsEvidenceSchema = z.object({
  id: z.string(),
  messageId: z.string(),
  type: z.string(),
  providerRef: z.string(),
  qualifiedTimestamp: z.string(),
  raw: z.string(),
  createdAt: z.string(),
});

export type QerdsEvidence = z.infer<typeof qerdsEvidenceSchema>;

// Attachment metadata only; the bytes are fetched via the download endpoint.
export const qerdsAttachmentSchema = z.object({
  id: z.string(),
  messageId: z.string(),
  filename: z.string(),
  contentType: z.string(),
  contentHash: z.string(),
  sizeBytes: z.number(),
  createdAt: z.string(),
});

export type QerdsAttachment = z.infer<typeof qerdsAttachmentSchema>;

export const qerdsMessageWithEvidenceSchema = qerdsMessageSchema.extend({
  attachments: z.array(qerdsAttachmentSchema),
  evidence: z.array(qerdsEvidenceSchema),
});

export type QerdsMessageWithEvidence = z.infer<
  typeof qerdsMessageWithEvidenceSchema
>;

export const qerdsAddressSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  address: z.string(),
  isDefault: z.boolean(),
  providerRef: z.string().optional(),
  createdAt: z.string(),
});

export type QerdsAddress = z.infer<typeof qerdsAddressSchema>;

const qerdsAddressListSchema = z.array(qerdsAddressSchema);

const qerdsPollResultSchema = z.object({ received: z.number() });

export type QerdsPollResult = z.infer<typeof qerdsPollResultSchema>;

export function getQerdsMessages(
  slug: string,
  signal?: AbortSignal,
): Promise<QerdsMessage[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/messages`, {
    schema: qerdsMessageListSchema,
    signal,
  });
}

export function getQerdsMessage(
  slug: string,
  messageId: string,
  signal?: AbortSignal,
): Promise<QerdsMessageWithEvidence> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/qerds/messages/${encodeURIComponent(messageId)}`,
    {
      schema: qerdsMessageWithEvidenceSchema,
      signal,
    },
  );
}

export function sendQerdsMessage(
  slug: string,
  input: {
    recipient: string;
    subject: string;
    body: string;
    attachments?: File[];
  },
  signal?: AbortSignal,
): Promise<QerdsMessage> {
  const form = new FormData();
  form.set("recipient", input.recipient);
  form.set("subject", input.subject);
  form.set("body", input.body);
  for (const file of input.attachments ?? []) {
    form.append("attachments", file, file.name);
  }
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/messages`, {
    schema: qerdsMessageSchema,
    method: "POST",
    body: form,
    signal,
  });
}

// Fetches an attachment's bytes and triggers a browser download. Content is
// fetched with credentials (matching the rest of the API) rather than via a
// plain link, so the session cookie is always sent.
export async function downloadQerdsAttachment(
  slug: string,
  messageId: string,
  attachment: QerdsAttachment,
  signal?: AbortSignal,
): Promise<void> {
  const { blob, filename } = await requestBlob(
    `/api/v1/orgs/${encodeURIComponent(slug)}/qerds/messages/${encodeURIComponent(messageId)}/attachments/${encodeURIComponent(attachment.id)}`,
    { signal },
  );
  const objectUrl = URL.createObjectURL(blob);
  try {
    const link = document.createElement("a");
    link.href = objectUrl;
    link.download = filename ?? attachment.filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}

export function pollQerdsInbox(
  slug: string,
  signal?: AbortSignal,
): Promise<QerdsPollResult> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/poll`, {
    schema: qerdsPollResultSchema,
    method: "POST",
    signal,
  });
}

export function getQerdsAddresses(
  slug: string,
  signal?: AbortSignal,
): Promise<QerdsAddress[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/addresses`, {
    schema: qerdsAddressListSchema,
    signal,
  });
}

export function createQerdsAddress(
  slug: string,
  input: { localPart?: string; default?: boolean },
  signal?: AbortSignal,
): Promise<QerdsAddress> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/addresses`, {
    schema: qerdsAddressSchema,
    method: "POST",
    body: input,
    signal,
  });
}
