// Environment-driven configuration for the PostGuard sidecar.
//
// The sidecar is an internal service: it is reachable only from the Go backend
// over the private Compose network and every request must carry the shared
// secret. `PG_SIDECAR_TOKEN` is therefore mandatory — the process refuses to start
// without it (fail closed) so a misconfiguration can never expose an
// unauthenticated encrypt-and-send endpoint.

const DEFAULT_PORT = 8090;
const DEFAULT_BIND = "0.0.0.0"; // reachable by the backend container; never published to the host
const DEFAULT_PKG_URL = "https://pkg.postguard.eu";
const DEFAULT_CRYPTIFY_URL = "https://storage.postguard.eu";
const DEFAULT_MAX_UPLOAD_BYTES = 100 * 1024 * 1024; // 100 MB per request

export interface Config {
  port: number;
  bindHost: string;
  /** Shared secret the Go backend must present as `Authorization: Bearer <token>`. */
  sharedSecret: string;
  pkgUrl: string;
  cryptifyUrl: string;
  maxUploadBytes: number;
}

function required(name: string): string {
  const value = process.env[name];
  if (value === undefined || value.trim() === "") {
    throw new Error(`postguard-sidecar: required environment variable ${name} is not set`);
  }
  return value;
}

function intOrDefault(name: string, fallback: number): number {
  const raw = process.env[name];
  if (raw === undefined || raw.trim() === "") {
    return fallback;
  }
  const parsed = Number.parseInt(raw, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`postguard-sidecar: ${name} must be a positive integer, got "${raw}"`);
  }
  return parsed;
}

export function loadConfig(): Config {
  return {
    port: intOrDefault("PG_SIDECAR_PORT", DEFAULT_PORT),
    bindHost: process.env.PG_SIDECAR_BIND?.trim() || DEFAULT_BIND,
    sharedSecret: required("PG_SIDECAR_TOKEN"),
    pkgUrl: process.env.PG_PKG_URL?.trim() || DEFAULT_PKG_URL,
    cryptifyUrl: process.env.PG_CRYPTIFY_URL?.trim() || DEFAULT_CRYPTIFY_URL,
    maxUploadBytes: intOrDefault("PG_MAX_UPLOAD_BYTES", DEFAULT_MAX_UPLOAD_BYTES),
  };
}
