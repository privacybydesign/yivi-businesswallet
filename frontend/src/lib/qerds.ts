export type QerdsStatusTone = "default" | "green" | "amber" | "red" | "blue";

// Maps a message status to a Tag tone. Delivered/read/accepted/received read as
// success, submitted as in-flight, failed/expired as error.
export function qerdsStatusTone(status: string): QerdsStatusTone {
  switch (status) {
    case "delivered":
    case "read":
    case "accepted":
    case "received":
      return "green";
    case "submitted":
      return "amber";
    case "failed":
    case "expired":
      return "red";
    default:
      return "default";
  }
}

// Maps a credential-offer's decision status (backend/internal/attestation's
// OfferPending/Accepting/Accepted/Declined) to a Tag tone. An offer not yet
// queued (no status at all) reads the same as pending: nothing has been
// decided either way.
export function credentialOfferStatusTone(status: string): QerdsStatusTone {
  switch (status) {
    case "accepted":
      return "green";
    case "declined":
      return "red";
    default:
      return "amber";
  }
}

const BYTE_UNITS = ["B", "KB", "MB", "GB"] as const;
const BYTES_PER_UNIT = 1024;

// Formats a byte count as a short human-readable size (e.g. "1.4 MB").
export function formatBytes(bytes: number): string {
  if (bytes < BYTES_PER_UNIT) {
    return `${bytes} ${BYTE_UNITS[0]}`;
  }
  let value = bytes;
  let unit = 0;
  while (value >= BYTES_PER_UNIT && unit < BYTE_UNITS.length - 1) {
    value /= BYTES_PER_UNIT;
    unit++;
  }
  return `${value.toFixed(1)} ${BYTE_UNITS[unit]}`;
}

// Evidence blobs arrive base64-encoded from the provider; decode for display,
// falling back to the raw string if it is not valid base64.
export function decodeEvidence(raw: string): string {
  try {
    return atob(raw);
  } catch {
    return raw;
  }
}
