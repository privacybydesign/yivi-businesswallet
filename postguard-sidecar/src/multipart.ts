// Streaming multipart/form-data parser for inbound requests from the Go backend.
//
// Files are buffered in memory and capped at `maxFileBytes`; exceeding the cap
// aborts parsing with a `PayloadTooLargeError` so the caller can return 413.

import busboy from "busboy";
import type { IncomingMessage } from "node:http";

export class PayloadTooLargeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PayloadTooLargeError";
  }
}

export class MalformedRequestError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MalformedRequestError";
  }
}

export interface ParsedFile {
  filename: string;
  mimeType: string;
  data: Buffer;
}

export interface ParsedForm {
  fields: Record<string, string>;
  files: ParsedFile[];
}

export function parseMultipart(req: IncomingMessage, maxFileBytes: number): Promise<ParsedForm> {
  return new Promise((resolve, reject) => {
    let bb: busboy.Busboy;
    try {
      bb = busboy({ headers: req.headers, limits: { fileSize: maxFileBytes } });
    } catch {
      reject(new MalformedRequestError("request is not valid multipart/form-data"));
      return;
    }

    const fields: Record<string, string> = {};
    const files: ParsedFile[] = [];
    let settled = false;

    const fail = (err: Error): void => {
      if (settled) return;
      settled = true;
      req.unpipe(bb);
      reject(err);
    };

    bb.on("field", (name, value) => {
      fields[name] = value;
    });

    bb.on("file", (_name, stream, info) => {
      const chunks: Buffer[] = [];
      stream.on("data", (chunk: Buffer) => chunks.push(chunk));
      stream.on("limit", () => {
        fail(new PayloadTooLargeError(`file "${info.filename}" exceeds the maximum allowed size`));
      });
      stream.on("close", () => {
        if (settled) return;
        files.push({
          filename: info.filename || "file",
          mimeType: info.mimeType || "application/octet-stream",
          data: Buffer.concat(chunks),
        });
      });
    });

    bb.on("error", (err) => fail(err instanceof Error ? err : new Error(String(err))));
    bb.on("close", () => {
      if (settled) return;
      settled = true;
      resolve({ fields, files });
    });

    req.on("aborted", () => fail(new MalformedRequestError("request aborted")));
    req.pipe(bb);
  });
}
