import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useCreateExportJobMutation,
  useExportJobsQuery,
  useSetDataInstructionMutation,
} from "../api/export.queries";
import { EXPORT_SECTIONS } from "../api/export";
import type { ExportJob, ExportSection } from "../api/export";
import { useOrganizationQuery } from "../api/organization.queries";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card, Tag } from "../ui";
import * as React from "react";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";

// Backend job statuses mapped to their copy and tone. A status this screen does
// not know renders as itself rather than a missing-translation key.
const STATUS_KEYS = {
  queued: "exportSettings.status.queued",
  running: "exportSettings.status.running",
  ready: "exportSettings.status.ready",
  failed: "exportSettings.status.failed",
} as const;

const STATUS_TONES: Record<string, "green" | "blue" | "red" | "default"> = {
  queued: "default",
  running: "blue",
  ready: "green",
  failed: "red",
};

function statusLabel(status: string, t: TFunction): string {
  const key = STATUS_KEYS[status as keyof typeof STATUS_KEYS];
  return key ? t(key) : status;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

export function ExportSettingsPanel({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();

  if (!isAdmin) {
    // The bundle carries every member's personal data, so the screen says why
    // rather than showing an empty panel.
    return (
      <Card>
        <p className={HINT}>{t("exportSettings.adminOnly")}</p>
      </Card>
    );
  }

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      <RequestExportCard slug={slug} />
      <DataInstructionCard slug={slug} />
    </div>
  );
}

function RequestExportCard({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const jobs = useExportJobsQuery(slug);
  const create = useCreateExportJobMutation(slug);
  // Empty means the whole bundle, which is what data portability asks for; the
  // filter is here for an admin who wants one data point.
  const [selected, setSelected] = useState<ExportSection[]>([]);

  const toggle = (section: ExportSection): void => {
    setSelected((prev) =>
      prev.includes(section)
        ? prev.filter((key) => key !== section)
        : [...prev, section],
    );
  };

  return (
    <Card>
      <div className="flex flex-col gap-4">
        <div>
          <h2 className="text-ink text-[15px] font-semibold">
            {t("exportSettings.requestTitle")}
          </h2>
          <p className={HINT}>{t("exportSettings.requestIntro")}</p>
        </div>

        <fieldset className="flex flex-col gap-2">
          <legend className={LABEL}>{t("exportSettings.sectionsLabel")}</legend>
          <p className={HINT}>{t("exportSettings.sectionsHint")}</p>
          {EXPORT_SECTIONS.map((section) => (
            <label
              key={section}
              className="flex items-start gap-2 text-[13.5px]"
            >
              <input
                type="checkbox"
                checked={selected.includes(section)}
                onChange={() => toggle(section)}
                className="mt-1"
              />
              <span>
                <span className="text-ink">
                  {t(`exportSettings.sections.${section}`)}
                </span>
                <span className={`block ${HINT}`}>
                  {t(`exportSettings.sectionHints.${section}`)}
                </span>
              </span>
            </label>
          ))}
        </fieldset>

        <div>
          <Button
            onClick={() => create.mutate(selected)}
            disabled={create.isPending}
          >
            {selected.length === 0
              ? t("exportSettings.requestAll")
              : t("exportSettings.requestSelected", { count: selected.length })}
          </Button>
        </div>

        <ExportHistory jobs={jobs.data ?? []} loading={jobs.isPending} />
      </div>
    </Card>
  );
}

function ExportHistory({
  jobs,
  loading,
}: {
  jobs: ExportJob[];
  loading: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const when = useWhenFormatter();

  if (loading) return <p className={HINT}>{t("common.loading")}</p>;
  if (jobs.length === 0) {
    return <p className={HINT}>{t("exportSettings.noExports")}</p>;
  }

  return (
    <div className="flex flex-col gap-2">
      <h3 className={LABEL}>{t("exportSettings.historyTitle")}</h3>
      <ul className="flex flex-col gap-2">
        {jobs.map((job) => (
          <li
            key={job.id}
            className="rounded-yivi border-line flex flex-wrap items-center gap-3 border px-3 py-2 text-[13px]"
          >
            <Tag tone={STATUS_TONES[job.status] ?? "default"}>
              {statusLabel(job.status, t)}
            </Tag>
            <span className="text-ink-soft">{when(job.createdAt)}</span>
            {job.sizeBytes > 0 && (
              <span className="text-ink-soft">
                {formatBytes(job.sizeBytes)}
              </span>
            )}
            {job.origin === "termination" && (
              <Tag tone="amber">{t("exportSettings.originTermination")}</Tag>
            )}
            {job.error && <span className="text-error">{job.error}</span>}
            {/* Present only while the bundle is actually fetchable, so a spent
                or expired export offers no dead link. */}
            {job.downloadPath && (
              <a
                href={job.downloadPath}
                className="text-link ml-auto font-semibold underline"
              >
                {t("exportSettings.download")}
              </a>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function DataInstructionCard({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const org = useOrganizationQuery(slug);
  const save = useSetDataInstructionMutation(slug);
  const current = org.data?.dataInstruction ?? "transfer";

  return (
    <Card>
      <div className="flex flex-col gap-4">
        <div>
          <h2 className="text-ink text-[15px] font-semibold">
            {t("exportSettings.instructionTitle")}
          </h2>
          {/* Asked now because termination is exactly the moment nobody can be
              asked. */}
          <p className={HINT}>{t("exportSettings.instructionIntro")}</p>
        </div>

        <div className="flex flex-col gap-2">
          {(["transfer", "delete"] as const).map((instruction) => (
            <label
              key={instruction}
              className="flex items-start gap-2 text-[13.5px]"
            >
              <input
                type="radio"
                name="dataInstruction"
                checked={current === instruction}
                onChange={() => save.mutate(instruction)}
                disabled={save.isPending}
                className="mt-1"
              />
              <span>
                <span className="text-ink">
                  {t(`exportSettings.instructions.${instruction}`)}
                </span>
                <span className={`block ${HINT}`}>
                  {t(`exportSettings.instructionHints.${instruction}`)}
                </span>
              </span>
            </label>
          ))}
        </div>

        {org.data?.erasurePendingAt && (
          <p className="text-warning-fg text-[13px]">
            {t("exportSettings.erasurePending")}
          </p>
        )}
      </div>
    </Card>
  );
}
