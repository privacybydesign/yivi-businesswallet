import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useProvisioningSettingsQuery,
  useRunProvisioningSyncMutation,
  useUpdateProvisioningSettingsMutation,
} from "../api/provisioning.queries";
import type {
  ProvisioningSettings,
  ProvisioningSyncResult,
} from "../api/provisioning";
import { errorCode } from "../lib/api-error";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card, Input } from "../ui";
import * as React from "react";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
// Mirrors the Input base so the multi-line group list reads as the same field.
const TEXTAREA_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 py-2 text-[13.5px] leading-relaxed transition-colors outline-none placeholder:text-muted focus:border-ink focus:ring-ink/10 focus:ring-3";

// The directory-source drivers this frontend knows a display name for. An id the
// backend reports that is not here still renders — as its raw id — so a new
// driver is usable before the frontend learns its name.
const SOURCE_LABELS: Record<string, string> = {
  entra: "Microsoft Entra ID",
};

function sourceLabel(id: string): string {
  return SOURCE_LABELS[id] ?? id;
}

// Backend enums (provisioning.RunSucceeded/RunFailed and the Skip* reasons)
// mapped to their copy keys. An unknown value falls back to the raw string
// rather than a missing-translation key, and the explicit map keeps the keys
// literal for the typed t().
const RUN_STATUS_KEYS = {
  succeeded: "provisioningSettings.runStatus.succeeded",
  failed: "provisioningSettings.runStatus.failed",
} as const;

const SKIP_REASON_KEYS = {
  incomplete: "provisioningSettings.skipReasons.incomplete",
  conflict: "provisioningSettings.skipReasons.conflict",
  last_admin: "provisioningSettings.skipReasons.last_admin",
  removed_locally: "provisioningSettings.skipReasons.removed_locally",
} as const;

function runStatusLabel(status: string, t: TFunction): string {
  const key = RUN_STATUS_KEYS[status as keyof typeof RUN_STATUS_KEYS];
  return key ? t(key) : status;
}

function skipReasonLabel(reason: string, t: TFunction): string {
  const key = SKIP_REASON_KEYS[reason as keyof typeof SKIP_REASON_KEYS];
  return key ? t(key) : reason;
}

// A sync fails with a small set of known codes; each names the thing to fix.
// Anything else falls back to the raw message.
function syncError(error: Error, t: TFunction): string {
  const code = errorCode(error);
  switch (code) {
    case "not_configured":
    case "disabled":
    case "unknown_source":
    case "incomplete_config":
    case "empty_directory":
    case "sync_timeout":
    case "sync_failed":
      return t(`provisioningSettings.syncErrors.${code}`);
    default:
      return t("provisioningSettings.syncError", { message: error.message });
  }
}

// A save fails with a small set of known codes; each names the thing to fix.
// Anything else falls back to the raw message.
function saveError(error: Error, t: TFunction): string {
  const code = errorCode(error);
  switch (code) {
    case "no_encryption_key":
      return t(`provisioningSettings.saveErrors.${code}`);
    default:
      return t("provisioningSettings.saveError", { message: error.message });
  }
}

// One admin-group id per line, both on the way in and the way out; the backend
// trims and de-duplicates, so this only has to be forgiving about whitespace.
function parseGroupIds(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((id) => id.trim())
    .filter((id) => id !== "");
}

// configFingerprint changes only when the saved configuration changes, so it is
// what the form is remounted on. It deliberately excludes updatedAt and the
// run-status fields: a sync run bumps updated_at too (RecordRun), and keying on
// that would remount the form mid-edit — an admin who pastes a rotated secret
// and clicks "Sync now" to test it would lose the paste, and the run would use
// the old stored secret. The sibling e-mail/Slack panels can key on updatedAt
// only because nothing but their save writes it.
function configFingerprint(s: ProvisioningSettings): string {
  return JSON.stringify([
    s.source,
    s.tenantId,
    s.clientId,
    s.hasClientSecret,
    s.groupId,
    s.adminGroupIds,
    s.enabled,
  ]);
}

// ProvisioningForm seeds its state from the stored settings, so it is remounted
// (via a `key` on configFingerprint) whenever a save refreshes the data.
function ProvisioningForm({
  slug,
  initial,
}: {
  slug: string;
  initial: ProvisioningSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useUpdateProvisioningSettingsMutation(slug);

  // sources is a required, backend-owned, non-empty list (provisioning.Sources),
  // so it is rendered as-is — no hardcoded fallback that would pin a driver id
  // here.
  const sources = initial.sources;
  const [enabled, setEnabled] = useState(initial.enabled);
  const [source, setSource] = useState(initial.source || sources[0]);
  const [tenantId, setTenantId] = useState(initial.tenantId);
  const [clientId, setClientId] = useState(initial.clientId);
  const [clientSecret, setClientSecret] = useState("");
  const [groupId, setGroupId] = useState(initial.groupId);
  const [adminGroups, setAdminGroups] = useState(
    initial.adminGroupIds.join("\n"),
  );
  const [localError, setLocalError] = useState<string | null>(null);

  function handleSave(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (save.isPending) {
      return;
    }
    const trimmedTenant = tenantId.trim();
    const trimmedClient = clientId.trim();
    // Enabling a sync without a credential only fails later, at run time, so
    // catch it here where the fields are. Disabled configs may be saved
    // half-filled.
    if (enabled) {
      if (!trimmedTenant || !trimmedClient) {
        setLocalError(t("provisioningSettings.credentialsRequired"));
        return;
      }
      if (!initial.hasClientSecret && clientSecret === "") {
        setLocalError(t("provisioningSettings.secretRequired"));
        return;
      }
    }
    setLocalError(null);
    save.mutate({
      enabled,
      source,
      tenantId: trimmedTenant,
      clientId: trimmedClient,
      // Blank keeps the stored secret; a typed value replaces it.
      clientSecret: clientSecret ? clientSecret : null,
      groupId: groupId.trim(),
      adminGroupIds: parseGroupIds(adminGroups),
    });
  }

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("provisioningSettings.heading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("provisioningSettings.description")}
      </p>

      <form onSubmit={handleSave} className="mt-4 flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-source" className={LABEL}>
            {t("provisioningSettings.source")}
          </label>
          <select
            id="provisioning-source"
            className={`${TEXTAREA_CLASS} h-9 py-0`}
            value={source}
            onChange={(event) => setSource(event.target.value)}
          >
            {sources.map((id) => (
              <option key={id} value={id}>
                {sourceLabel(id)}
              </option>
            ))}
          </select>
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-tenant" className={LABEL}>
            {t("provisioningSettings.tenantId")}
          </label>
          <Input
            id="provisioning-tenant"
            value={tenantId}
            onChange={(event) => setTenantId(event.target.value)}
            placeholder={t("provisioningSettings.tenantIdPlaceholder")}
            autoComplete="off"
          />
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-client" className={LABEL}>
            {t("provisioningSettings.clientId")}
          </label>
          <Input
            id="provisioning-client"
            value={clientId}
            onChange={(event) => setClientId(event.target.value)}
            placeholder={t("provisioningSettings.clientIdPlaceholder")}
            autoComplete="off"
          />
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-secret" className={LABEL}>
            {t("provisioningSettings.clientSecret")}
          </label>
          <Input
            id="provisioning-secret"
            type="password"
            value={clientSecret}
            onChange={(event) => setClientSecret(event.target.value)}
            autoComplete="new-password"
            placeholder={
              initial.hasClientSecret
                ? t("provisioningSettings.secretUnchanged")
                : t("provisioningSettings.secretPlaceholder")
            }
          />
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-group" className={LABEL}>
            {t("provisioningSettings.groupId")}
          </label>
          <Input
            id="provisioning-group"
            value={groupId}
            onChange={(event) => setGroupId(event.target.value)}
            placeholder={t("provisioningSettings.groupIdPlaceholder")}
            autoComplete="off"
            aria-describedby="provisioning-group-hint"
          />
          <span id="provisioning-group-hint" className={HINT}>
            {t("provisioningSettings.groupIdHint")}
          </span>
        </div>

        <div className="flex flex-col gap-1">
          <label htmlFor="provisioning-admin-groups" className={LABEL}>
            {t("provisioningSettings.adminGroupIds")}
          </label>
          <textarea
            id="provisioning-admin-groups"
            className={`${TEXTAREA_CLASS} min-h-20`}
            value={adminGroups}
            onChange={(event) => setAdminGroups(event.target.value)}
            placeholder={t("provisioningSettings.adminGroupIdsPlaceholder")}
            aria-describedby="provisioning-admin-groups-hint"
          />
          <span id="provisioning-admin-groups-hint" className={HINT}>
            {t("provisioningSettings.adminGroupIdsHint")}
          </span>
        </div>

        <label className="text-ink flex cursor-pointer items-center gap-2 text-[13.5px]">
          <input
            id="provisioning-enabled"
            type="checkbox"
            checked={enabled}
            onChange={(event) => setEnabled(event.target.checked)}
            aria-describedby="provisioning-enabled-hint"
          />
          {t("provisioningSettings.enabled")}
        </label>
        <span id="provisioning-enabled-hint" className={HINT}>
          {t("provisioningSettings.enabledHint")}
        </span>

        {localError && (
          <p role="alert" className="text-error text-[13px]">
            {localError}
          </p>
        )}
        {save.isError && (
          <p role="alert" className="text-error text-[13px]">
            {saveError(save.error, t)}
          </p>
        )}

        <div>
          <Button type="submit" loading={save.isPending}>
            {t("common.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

function summarizeResult(result: ProvisioningSyncResult, t: TFunction): string {
  return t("provisioningSettings.resultSummary", {
    invited: result.membersInvited,
    updated: result.membersUpdated,
    removed: result.membersRemoved,
    departments: result.departmentsCreated,
  });
}

// Skips are grouped by reason so the panel reports "3 conflicts" rather than a
// list of e-mail addresses (the backend deliberately keeps those out of audit
// metadata for the same reason).
function skipCounts(result: ProvisioningSyncResult): [string, number][] {
  const counts = new Map<string, number>();
  for (const skip of result.skipped) {
    counts.set(skip.reason, (counts.get(skip.reason) ?? 0) + 1);
  }
  return [...counts.entries()];
}

function SyncCard({
  slug,
  settings,
}: {
  slug: string;
  settings: ProvisioningSettings;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const sync = useRunProvisioningSyncMutation(slug);

  const canSync = settings.configured && settings.enabled;

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("provisioningSettings.syncHeading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("provisioningSettings.syncDescription")}
      </p>

      {settings.lastRunStatus && (
        <p className="mt-3 text-[13px]">
          <span className={LABEL}>{t("provisioningSettings.lastRun")}</span>{" "}
          <span
            className={
              settings.lastRunStatus === "failed" ? "text-error" : "text-ink"
            }
          >
            {runStatusLabel(settings.lastRunStatus, t)}
          </span>
          {settings.lastRunAt && (
            <span className="text-ink-soft">
              {" · "}
              {formatWhen(settings.lastRunAt)}
            </span>
          )}
        </p>
      )}
      {settings.lastRunError && (
        <p className="text-error mt-1 text-[13px]">
          {t("provisioningSettings.lastRunError", {
            message: settings.lastRunError,
          })}
        </p>
      )}

      <div className="mt-4">
        <Button
          onClick={() => sync.mutate()}
          loading={sync.isPending}
          disabled={!canSync}
        >
          {t("provisioningSettings.syncNow")}
        </Button>
        {!canSync && (
          <p className={`${HINT} mt-2`}>
            {t("provisioningSettings.syncDisabled")}
          </p>
        )}
      </div>

      {sync.isSuccess && (
        <div className="border-line mt-4 border-t pt-3 text-[13px]">
          <p className="text-ink">{summarizeResult(sync.data, t)}</p>
          {skipCounts(sync.data).length > 0 && (
            <ul className="text-ink-soft mt-2 flex flex-col gap-1">
              {skipCounts(sync.data).map(([reason, count]) => (
                <li key={reason}>
                  {t("provisioningSettings.skipCount", {
                    count,
                    reason: skipReasonLabel(reason, t),
                  })}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {sync.isError && (
        <p role="alert" className="text-error mt-3 text-[13px]">
          {syncError(sync.error, t)}
        </p>
      )}
    </Card>
  );
}

// Static setup guidance for the Microsoft Entra app registration. It repeats no
// tenant-specific values — just the permissions an admin must grant and consent
// to before the credential above will work.
function EntraGuidanceCard(): React.JSX.Element {
  const { t } = useTranslation();
  const steps = t("provisioningSettings.setup.steps", {
    returnObjects: true,
  }) as readonly string[];
  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">
        {t("provisioningSettings.setup.heading")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("provisioningSettings.setup.intro")}
      </p>
      <ol className="text-ink mt-3 flex list-decimal flex-col gap-1.5 pl-5 text-[13px]">
        {steps.map((step, index) => (
          <li key={index}>{step}</li>
        ))}
      </ol>
    </Card>
  );
}

// ProvisioningSettingsPanel is the directory-sync configuration, rendered as a
// tab on the Settings page. The caller (Settings) has already resolved the org
// and gated on admin, so the panel only owns the settings query and its forms.
export function ProvisioningSettingsPanel({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = useProvisioningSettingsQuery(slug, true);

  if (settings.isError) {
    return (
      <Card className="p-6">
        <p className="text-error text-[14px]">
          {t("provisioningSettings.loadError", {
            message: settings.error.message,
          })}
        </p>
      </Card>
    );
  }
  if (settings.isPending) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <ProvisioningForm
        key={configFingerprint(settings.data)}
        slug={slug}
        initial={settings.data}
      />
      <SyncCard slug={slug} settings={settings.data} />
      <EntraGuidanceCard />
    </div>
  );
}
