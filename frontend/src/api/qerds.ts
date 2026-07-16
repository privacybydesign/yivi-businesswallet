import { z } from "zod";
import { request } from "./http";

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

export const qerdsMessageWithEvidenceSchema = qerdsMessageSchema.extend({
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

export const qerdsContactSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  name: z.string(),
  address: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type QerdsContact = z.infer<typeof qerdsContactSchema>;

const qerdsContactListSchema = z.array(qerdsContactSchema);

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
  input: { sender?: string; recipient: string; subject: string; body: string },
  signal?: AbortSignal,
): Promise<QerdsMessage> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/messages`, {
    schema: qerdsMessageSchema,
    method: "POST",
    body: input,
    signal,
  });
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

export function setDefaultQerdsAddress(
  slug: string,
  addressId: string,
  signal?: AbortSignal,
): Promise<QerdsAddress> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/qerds/addresses/${encodeURIComponent(addressId)}/default`,
    {
      schema: qerdsAddressSchema,
      method: "POST",
      signal,
    },
  );
}

export function getQerdsContacts(
  slug: string,
  signal?: AbortSignal,
): Promise<QerdsContact[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/contacts`, {
    schema: qerdsContactListSchema,
    signal,
  });
}

export function createQerdsContact(
  slug: string,
  input: { name: string; address: string },
  signal?: AbortSignal,
): Promise<QerdsContact> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/qerds/contacts`, {
    schema: qerdsContactSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function deleteQerdsContact(
  slug: string,
  contactId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/qerds/contacts/${encodeURIComponent(contactId)}`,
    {
      schema: z.void(),
      method: "DELETE",
      signal,
    },
  );
}
