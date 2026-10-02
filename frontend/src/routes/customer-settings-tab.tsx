import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useRemoveProofingCustomerMutation,
  useUpdateProofingCustomerMutation,
} from "../api/identity-proofing.queries";
import type { ProofingCustomer } from "../api/identity-proofing";
import {
  DATA_RETENTION_DAY_OPTIONS,
  SESSION_TTL_OPTIONS_SECONDS,
  proofingErrorMessage,
  ttlMinutes,
} from "../lib/identity-proofing";
import { originLines } from "../lib/hosted-completion";
import { Button, Card, ConfirmDialog, Input } from "../ui";

const LABEL = "text-ink text-[13px] font-semibold";
const HINT = "text-muted text-[12px]";
const ERROR = "text-error text-[12.5px]";

// A customer's settings: how long a mailed session runs, how long an approved
// subject's name is kept, where its hosted pages may hand the subject back, its
// name, and removing it.
export function SettingsTab({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateProofingCustomerMutation(slug, customer.id);

  return (
    <div className="flex max-w-3xl flex-col gap-5">
      <Card>
        <h2 className="font-display border-line border-b px-5 py-4 text-[17px] font-bold">
          {t("customers.settings.sessionsTitle")}
        </h2>
        <div className="divide-line divide-y px-5">
          <SettingRow
            label={t("customers.settings.qrLifetime")}
            hint={t("customers.settings.qrLifetimeHint")}
          >
            <Segmented
              label={t("customers.settings.qrLifetime")}
              options={SESSION_TTL_OPTIONS_SECONDS.map((seconds) => ({
                value: seconds,
                label: t("customers.settings.minutes", {
                  count: ttlMinutes(seconds),
                }),
              }))}
              value={customer.sessionTtlSeconds}
              disabled={update.isPending}
              onChange={(seconds) =>
                update.mutate({ sessionTtlSeconds: seconds })
              }
            />
          </SettingRow>
          <SettingRow
            label={t("customers.settings.retention")}
            hint={t("customers.settings.retentionHint")}
          >
            <Segmented
              label={t("customers.settings.retention")}
              options={DATA_RETENTION_DAY_OPTIONS.map((days) => ({
                value: days,
                label: t("customers.settings.days", { count: days }),
              }))}
              value={customer.dataRetentionDays}
              disabled={update.isPending}
              onChange={(days) => update.mutate({ dataRetentionDays: days })}
            />
          </SettingRow>
        </div>
        {update.isError && (
          <p className={`${ERROR} px-5 pb-4`}>
            {proofingErrorMessage(update.error, t)}
          </p>
        )}
      </Card>
      <RedirectOriginsCard slug={slug} customer={customer} />
      <NameCard slug={slug} customer={customer} />
      <RemoveCard slug={slug} customer={customer} />
    </div>
  );
}

function SettingRow({
  label,
  hint,
  children,
}: {
  label: string;
  hint: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className="grid gap-3 py-4 md:grid-cols-[240px_minmax(0,1fr)] md:items-center">
      <div>
        <div className={LABEL}>{label}</div>
        <div className={`${HINT} mt-0.5`}>{hint}</div>
      </div>
      {children}
    </div>
  );
}

function Segmented<T extends number>({
  label,
  options,
  value,
  disabled,
  onChange,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: number;
  disabled: boolean;
  onChange: (value: T) => void;
}): React.JSX.Element {
  return (
    <div role="radiogroup" aria-label={label} className="flex flex-wrap gap-2">
      {options.map((option) => {
        const active = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={disabled}
            onClick={() => !active && onChange(option.value)}
            className={[
              "h-9 rounded-md border px-3.5 text-[13px] font-semibold whitespace-nowrap transition-colors disabled:opacity-60",
              active
                ? "border-ink bg-ink text-surface"
                : "border-line-strong bg-surface text-ink-soft hover:text-ink",
            ].join(" ")}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

const TEXTAREA =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 min-h-20 w-full max-w-xl border px-3 py-2 font-mono text-[13px] outline-none focus:ring-3";

// The origins a hosted page may redirect its subject to and be embedded on.
function RedirectOriginsCard({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateProofingCustomerMutation(slug, customer.id);
  const saved = customer.allowedRedirectOrigins.join("\n");
  const [text, setText] = useState(saved);
  const fieldId = `redirect-origins-${customer.id}`;

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    update.mutate(
      { allowedRedirectOrigins: originLines(text) },
      // Shows the origins as the backend normalised them.
      { onSuccess: (next) => setText(next.allowedRedirectOrigins.join("\n")) },
    );
  }

  return (
    <Card className="px-5 py-4">
      <label
        htmlFor={fieldId}
        className="font-display block text-[15px] font-bold"
      >
        {t("customers.settings.redirectOriginsTitle")}
      </label>
      <p className={`${HINT} mt-0.5`}>
        {t("customers.settings.redirectOriginsHint")}
      </p>
      <form className="mt-3 flex flex-col gap-3" onSubmit={submit} noValidate>
        <textarea
          id={fieldId}
          value={text}
          rows={3}
          spellCheck={false}
          placeholder="https://portal.example.com"
          className={TEXTAREA}
          onChange={(event) => setText(event.target.value)}
        />
        <div>
          <Button
            type="submit"
            loading={update.isPending}
            disabled={originLines(text).join("\n") === saved}
          >
            {t("customers.settings.save")}
          </Button>
        </div>
      </form>
      {update.isError && (
        <p className={`${ERROR} mt-2`}>
          {proofingErrorMessage(update.error, t)}
        </p>
      )}
    </Card>
  );
}

function NameCard({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateProofingCustomerMutation(slug, customer.id);
  const [name, setName] = useState(customer.name);
  const trimmed = name.trim();

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (trimmed !== "") {
      update.mutate({ name: trimmed });
    }
  }

  return (
    <Card className="px-5 py-4">
      <h2 className="font-display text-[15px] font-bold">
        {t("customers.settings.nameTitle")}
      </h2>
      <p className={`${HINT} mt-0.5`}>{t("customers.settings.nameHint")}</p>
      <form
        className="mt-3 flex flex-wrap items-end gap-3"
        onSubmit={submit}
        noValidate
      >
        <Input
          aria-label={t("customers.settings.nameTitle")}
          value={name}
          className="max-w-sm"
          onChange={(event) => setName(event.target.value)}
        />
        <Button
          type="submit"
          loading={update.isPending}
          disabled={trimmed === "" || trimmed === customer.name}
        >
          {t("customers.settings.save")}
        </Button>
      </form>
      {update.isError && (
        <p className={`${ERROR} mt-2`}>
          {proofingErrorMessage(update.error, t)}
        </p>
      )}
    </Card>
  );
}

function RemoveCard({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const remove = useRemoveProofingCustomerMutation(slug, customer.id);
  const [confirming, setConfirming] = useState(false);

  return (
    <Card className="border-error/40 flex flex-wrap items-center justify-between gap-4 px-5 py-4">
      <div className="min-w-0 flex-1 basis-64">
        <h2 className="font-display text-[15px] font-bold">
          {t("customers.settings.removeTitle")}
        </h2>
        <p className={`${HINT} mt-0.5`}>{t("customers.settings.removeHint")}</p>
        {remove.isError && (
          <p className={`${ERROR} mt-1`}>
            {proofingErrorMessage(remove.error, t)}
          </p>
        )}
      </div>
      <Button
        variant="dangerGhost"
        icon="delete"
        className="border-error text-error shrink-0 border"
        onClick={() => setConfirming(true)}
      >
        {t("customers.settings.remove")}
      </Button>
      {confirming && (
        <ConfirmDialog
          title={t("customers.settings.removeConfirm.title", {
            name: customer.name,
          })}
          message={t("customers.settings.removeConfirm.message")}
          confirmLabel={t("customers.settings.remove")}
          busy={remove.isPending}
          onConfirm={() =>
            remove.mutate(undefined, {
              onSuccess: () =>
                void navigate(`/${slug}/identity-proofing/customers`, {
                  replace: true,
                }),
            })
          }
          onClose={() => setConfirming(false)}
        />
      )}
    </Card>
  );
}
