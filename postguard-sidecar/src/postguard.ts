// Thin wrapper around @e4a/pg-js for the one operation this sidecar performs:
// encrypt one or more files to a set of e-mail recipients and upload the sealed
// package, authenticating the sender with a PostGuard for Business API key.
//
// The sidecar is deliberately stateless: the API key arrives with every request
// (supplied by the Go backend, which owns it at rest) and is never persisted here.

import { PostGuard } from "@e4a/pg-js";
import type { Config } from "./config.js";
import type { ParsedFile } from "./multipart.js";

export class UpstreamError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "UpstreamError";
  }
}

export interface SendRequest {
  apiKey: string;
  recipients: string[];
  files: ParsedFile[];
  notify: boolean;
  message?: string;
  language?: string;
}

export interface SendResult {
  uuid: string;
}

export class PostGuardClient {
  private readonly pg: PostGuard;

  constructor(config: Config) {
    this.pg = new PostGuard({ pkgUrl: config.pkgUrl, cryptifyUrl: config.cryptifyUrl });
  }

  async encryptAndSend(req: SendRequest): Promise<SendResult> {
    // The buffers are already fully in memory (bounded by the upload cap). Cast
    // Node's Buffer to BlobPart — the File/Blob constructor accepts it at
    // runtime; the mismatch is only in the DOM lib's ArrayBuffer generic.
    const files = req.files.map(
      (f) => new File([f.data as unknown as BlobPart], f.filename, { type: f.mimeType }),
    );
    const language = req.language === "NL" ? "NL" : "EN";

    try {
      const sealed = this.pg.encrypt({
        files,
        recipients: req.recipients.map((email) => this.pg.recipient.email(email)),
        sign: this.pg.sign.apiKey(req.apiKey),
      });

      const { uuid } = await sealed.upload({
        notify: {
          recipients: req.notify,
          sender: false,
          message: req.message,
          language,
        },
      });

      return { uuid };
    } catch (err) {
      throw new UpstreamError("PostGuard encrypt/upload failed", { cause: err });
    }
  }
}
