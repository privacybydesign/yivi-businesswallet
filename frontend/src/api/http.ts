import type { z } from "zod";

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? "";

const DEFAULT_TIMEOUT_MS = 30_000;
const DEFAULT_MAX_RETRIES = 2;
const RETRY_BASE_DELAY_MS = 300;
const RETRY_MAX_DELAY_MS = 3_000;
const SERVER_ERROR_MIN_STATUS = 500;
const NO_CONTENT_STATUS = 204;

/** HTTP methods supported by the client. Only GET is retried (idempotent). */
export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

const IDEMPOTENT_METHOD: HttpMethod = "GET";

export interface RequestOptions<T> {
  /** Zod schema used to validate and type the JSON response body. */
  schema: z.ZodType<T>;
  method?: HttpMethod;
  /** Plain object serialized as a JSON request body. */
  body?: unknown;
  headers?: Record<string, string>;
  /** Caller-supplied abort signal, merged with the internal timeout. */
  signal?: AbortSignal;
  /** Per-request timeout override in milliseconds. */
  timeoutMs?: number;
  /** Per-request retry-count override. Ignored for non-GET methods. */
  maxRetries?: number;
}

/** Thrown when the server responds with a non-2xx status. */
export class ApiError extends Error {
  readonly status: number;
  readonly statusText: string;
  readonly url: string;
  readonly body: unknown;

  constructor(status: number, statusText: string, url: string, body: unknown) {
    super(`Request to ${url} failed with status ${status} ${statusText}`);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.url = url;
    this.body = body;
  }
}

/** Thrown when the response body does not match the expected schema. */
export class ApiValidationError extends Error {
  readonly url: string;
  readonly issues: z.ZodIssue[];

  constructor(url: string, issues: z.ZodIssue[]) {
    super(`Response from ${url} did not match the expected schema`);
    this.name = "ApiValidationError";
    this.url = url;
    this.issues = issues;
  }
}

/**
 * Returns headers used to authenticate requests.
 *
 * Stub for the upcoming Yivi authentication: today it injects nothing. When
 * auth lands, return the Authorization header here and handle 401s centrally
 * in `request` (see the marked spot below).
 */
function getAuthHeaders(): Record<string, string> {
  return {};
}

function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason as Error);
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        reject(signal.reason as Error);
      },
      { once: true },
    );
  });
}

function backoffDelayMs(attempt: number): number {
  const exponential = RETRY_BASE_DELAY_MS * 2 ** attempt;
  return Math.min(exponential, RETRY_MAX_DELAY_MS);
}

async function parseBody(res: Response): Promise<unknown> {
  if (res.status === NO_CONTENT_STATUS) {
    return undefined;
  }
  const text = await res.text();
  if (text === "") {
    return undefined;
  }
  return JSON.parse(text) as unknown;
}

async function performFetch<T>(
  url: string,
  method: HttpMethod,
  options: RequestOptions<T>,
): Promise<T> {
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);

  const onExternalAbort = () => controller.abort(options.signal?.reason);
  options.signal?.addEventListener("abort", onExternalAbort, { once: true });

  const hasBody = options.body !== undefined;
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...getAuthHeaders(),
    ...options.headers,
  };
  if (hasBody) {
    headers["Content-Type"] = "application/json";
  }

  try {
    const res = await fetch(url, {
      method,
      headers,
      body: hasBody ? JSON.stringify(options.body) : undefined,
      signal: controller.signal,
    });

    const body = await parseBody(res);

    if (!res.ok) {
      // Future: when Yivi auth lands, handle res.status === 401 here
      // (clear session / trigger re-auth) before throwing.
      throw new ApiError(res.status, res.statusText, url, body);
    }

    const parsed = options.schema.safeParse(body);
    if (!parsed.success) {
      throw new ApiValidationError(url, parsed.error.issues);
    }
    return parsed.data;
  } finally {
    clearTimeout(timeout);
    options.signal?.removeEventListener("abort", onExternalAbort);
  }
}

function isRetryable(error: unknown): boolean {
  // Network/connection failures surface as TypeError from fetch.
  if (error instanceof TypeError) {
    return true;
  }
  if (error instanceof ApiError) {
    return error.status >= SERVER_ERROR_MIN_STATUS;
  }
  return false;
}

/**
 * Performs a typed, validated HTTP request against the API.
 *
 * - Prefixes `VITE_API_BASE_URL` (empty string uses the Vite dev proxy).
 * - Validates the JSON response with the provided zod `schema`.
 * - Retries idempotent GETs on network errors and 5xx with capped backoff.
 * - Throws {@link ApiError} on non-2xx and {@link ApiValidationError} on
 *   schema mismatch; neither validation errors nor non-GET methods are retried.
 */
export async function request<T>(
  path: string,
  options: RequestOptions<T>,
): Promise<T> {
  const url = `${API_BASE_URL}${path}`;
  const method = options.method ?? IDEMPOTENT_METHOD;
  const maxRetries =
    method === IDEMPOTENT_METHOD
      ? (options.maxRetries ?? DEFAULT_MAX_RETRIES)
      : 0;

  let lastError: unknown;
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      return await performFetch(url, method, options);
    } catch (error) {
      lastError = error;
      const canRetry = attempt < maxRetries && isRetryable(error);
      if (!canRetry) {
        throw error;
      }
      await delay(backoffDelayMs(attempt), options.signal);
    }
  }
  throw lastError;
}
