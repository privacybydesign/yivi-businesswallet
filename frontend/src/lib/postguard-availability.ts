import { ApiError } from "../api/http";
import type { PostguardSettings } from "../api/postguard";

const SERVICE_UNAVAILABLE_STATUS = 503;
// Returned by the backend when the deployment has no PostGuard master key.
const DEPLOYMENT_OFF_CODE = "postguard_not_configured";

/**
 * PostGuard usability for an org, derived from the settings query.
 *
 * - `loading`       — settings still in flight.
 * - `ready`         — both the encryption key and API key are configured.
 * - `unconfigured`  — the org is missing at least one key.
 * - `deploymentOff` — PostGuard is not enabled on this deployment.
 * - `error`         — settings could not be loaded for another reason.
 */
export type PostguardReadiness =
  | "loading"
  | "ready"
  | "unconfigured"
  | "deploymentOff"
  | "error";

export function postguardReadiness(
  settings: PostguardSettings | undefined,
  error: Error | null,
  isPending: boolean,
): PostguardReadiness {
  if (settings) {
    return settings.encryptionKey.configured && settings.apiKey.configured
      ? "ready"
      : "unconfigured";
  }
  if (isPending) {
    return "loading";
  }
  if (isDeploymentOff(error)) {
    return "deploymentOff";
  }
  return "error";
}

function isDeploymentOff(error: Error | null): boolean {
  return (
    error instanceof ApiError &&
    error.status === SERVICE_UNAVAILABLE_STATUS &&
    typeof error.body === "object" &&
    error.body !== null &&
    "code" in error.body &&
    error.body.code === DEPLOYMENT_OFF_CODE
  );
}
