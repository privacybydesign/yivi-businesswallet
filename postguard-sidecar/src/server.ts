// PostGuard sidecar HTTP server.
//
// Endpoints:
//   GET  /healthz  — unauthenticated liveness probe (used by Compose).
//   POST /v1/send  — authenticated encrypt-and-upload; multipart/form-data.
//
// Security posture: this service is never published to the host and is reachable
// only from the Go backend over the private Compose network. Every /v1/* request
// must additionally present the shared secret as `Authorization: Bearer <token>`,
// compared in constant time. The process exits if the secret is unset.

import { createHash, timingSafeEqual } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { loadConfig } from "./config.js";
import {
  MalformedRequestError,
  parseMultipart,
  PayloadTooLargeError,
  type ParsedForm,
} from "./multipart.js";
import { PostGuardClient, UpstreamError } from "./postguard.js";

const config = loadConfig();
const client = new PostGuardClient(config);

function sendJSON(res: ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(payload);
}

function sendError(res: ServerResponse, status: number, code: string, message: string): void {
  sendJSON(res, status, { error: code, message });
}

// Constant-time comparison that is also safe across differing lengths by
// comparing fixed-size digests rather than the raw tokens.
function secretMatches(presented: string): boolean {
  const a = createHash("sha256").update(presented).digest();
  const b = createHash("sha256").update(config.sharedSecret).digest();
  return timingSafeEqual(a, b);
}

function isAuthorized(req: IncomingMessage): boolean {
  const header = req.headers.authorization;
  if (!header || !header.startsWith("Bearer ")) {
    return false;
  }
  return secretMatches(header.slice("Bearer ".length));
}

function parseRecipients(raw: string | undefined): string[] {
  if (!raw) {
    throw new MalformedRequestError("recipients field is required");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new MalformedRequestError("recipients must be a JSON array of e-mail addresses");
  }
  if (!Array.isArray(parsed) || parsed.length === 0) {
    throw new MalformedRequestError("at least one recipient is required");
  }
  const recipients = parsed.map((r) => (typeof r === "string" ? r.trim() : ""));
  if (recipients.some((r) => r === "")) {
    throw new MalformedRequestError("recipients must be non-empty strings");
  }
  return recipients;
}

async function handleSend(req: IncomingMessage, res: ServerResponse): Promise<void> {
  let form: ParsedForm;
  try {
    form = await parseMultipart(req, config.maxUploadBytes);
  } catch (err) {
    if (err instanceof PayloadTooLargeError) {
      sendError(res, 413, "payload_too_large", err.message);
      return;
    }
    if (err instanceof MalformedRequestError) {
      sendError(res, 400, "malformed_request", err.message);
      return;
    }
    throw err;
  }

  const apiKey = form.fields.apiKey?.trim();
  if (!apiKey) {
    sendError(res, 400, "missing_api_key", "apiKey field is required");
    return;
  }
  if (form.files.length === 0) {
    sendError(res, 400, "no_files", "at least one file is required");
    return;
  }

  let recipients: string[];
  try {
    recipients = parseRecipients(form.fields.recipients);
  } catch (err) {
    sendError(res, 400, "invalid_recipients", (err as Error).message);
    return;
  }

  const result = await client.encryptAndSend({
    apiKey,
    recipients,
    files: form.files,
    notify: form.fields.notify !== "false",
    message: form.fields.message,
    language: form.fields.language,
  });

  sendJSON(res, 200, { uuid: result.uuid });
}

const server = createServer((req, res) => {
  void (async () => {
    try {
      if (req.method === "GET" && req.url === "/healthz") {
        sendJSON(res, 200, { status: "ok" });
        return;
      }

      if (req.method === "POST" && req.url === "/v1/send") {
        if (!isAuthorized(req)) {
          sendError(res, 401, "unauthorized", "missing or invalid bearer token");
          return;
        }
        await handleSend(req, res);
        return;
      }

      sendError(res, 404, "not_found", "unknown route");
    } catch (err) {
      const upstream = err instanceof UpstreamError;
      // Never leak internals to the caller; log them for operators instead.
      console.error("postguard-sidecar: request failed", err);
      if (!res.headersSent) {
        sendError(
          res,
          upstream ? 502 : 500,
          upstream ? "upstream_error" : "internal_error",
          upstream ? "PostGuard rejected the request" : "internal error",
        );
      }
    }
  })();
});

server.listen(config.port, config.bindHost, () => {
  console.log(
    `postguard-sidecar listening on ${config.bindHost}:${config.port} ` +
      `(pkg=${config.pkgUrl}, cryptify=${config.cryptifyUrl}, maxUpload=${config.maxUploadBytes}B)`,
  );
});
