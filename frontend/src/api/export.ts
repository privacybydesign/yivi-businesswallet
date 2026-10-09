import { z } from "zod";
import { absoluteApiUrl, request } from "./http";
import { organizationSchema } from "./organization";
import type { Organization } from "./organization";

// The bundle's data points. Kept in the same order the backend writes them so
// the screen's checkboxes read like the manifest.
export const EXPORT_SECTIONS = [
  "ownerIdentification",
  "attestations",
  "qerds",
  "auditRecords",
] as const;

export type ExportSection = (typeof EXPORT_SECTIONS)[number];

// Job status is a plain string rather than a closed enum: the set belongs to the
// backend, and a status the screen does not recognise should render as itself
// instead of failing the whole document.
export const exportJobSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  status: z.string(),
  origin: z.string(),
  sections: z.array(z.string()),
  requestedBy: z.string().optional(),
  bundleId: z.string().optional(),
  filename: z.string().optional(),
  sizeBytes: z.number(),
  checksum: z.string().optional(),
  downloadedAt: z.string().optional(),
  error: z.string().optional(),
  expiresAt: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
  // Present only while the bundle is actually fetchable, so the screen never
  // offers a link to something spent or expired.
  downloadPath: z.string().optional(),
});

export type ExportJob = z.infer<typeof exportJobSchema>;

const exportJobListSchema = z.array(exportJobSchema);

// The download path is an API path; make it absolute so the browser fetches it
// from the API origin even when the SPA is served elsewhere.
function withAbsoluteDownload(job: ExportJob): ExportJob {
  return job.downloadPath
    ? { ...job, downloadPath: absoluteApiUrl(job.downloadPath) }
    : job;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/export`;
}

export function listExportJobs(
  slug: string,
  signal?: AbortSignal,
): Promise<ExportJob[]> {
  return request(`${base(slug)}/jobs`, {
    schema: exportJobListSchema,
    signal,
  }).then((jobs) => jobs.map(withAbsoluteDownload));
}

export function getExportJob(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<ExportJob> {
  return request(`${base(slug)}/jobs/${encodeURIComponent(id)}`, {
    schema: exportJobSchema,
    signal,
  }).then(withAbsoluteDownload);
}

// An empty sections list means the whole bundle; the backend refuses an unknown
// key rather than quietly returning less.
export function createExportJob(
  slug: string,
  sections: ExportSection[],
  signal?: AbortSignal,
): Promise<ExportJob> {
  const query =
    sections.length > 0
      ? `?sections=${encodeURIComponent(sections.join(","))}`
      : "";
  return request(`${base(slug)}/jobs${query}`, {
    schema: exportJobSchema,
    method: "POST",
    signal,
  }).then(withAbsoluteDownload);
}

export const DATA_INSTRUCTIONS = ["transfer", "delete"] as const;

export type DataInstruction = (typeof DATA_INSTRUCTIONS)[number];

export function setDataInstruction(
  slug: string,
  dataInstruction: DataInstruction,
  signal?: AbortSignal,
): Promise<Organization> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/data-instruction`, {
    schema: organizationSchema,
    method: "PUT",
    body: { dataInstruction },
    signal,
  });
}
