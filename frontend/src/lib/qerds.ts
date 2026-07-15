export type QerdsStatusTone = "default" | "green" | "amber" | "red" | "blue";

// Maps a message status to a Tag tone. Delivered/read/accepted read as success,
// submitted as in-flight, received as informational, failed/expired as error.
export function qerdsStatusTone(status: string): QerdsStatusTone {
  switch (status) {
    case "delivered":
    case "read":
    case "accepted":
      return "green";
    case "submitted":
      return "amber";
    case "received":
      return "blue";
    case "failed":
    case "expired":
      return "red";
    default:
      return "default";
  }
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
