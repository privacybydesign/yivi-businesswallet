import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useCreateCustomerMutation } from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingRequest,
} from "../api/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import { useWhenFormatter } from "../lib/format-when";
import {
  customerDisplayStatus,
  isHexColor,
  proofingErrorMessage,
  proofingRejectionReason,
  proofingStatusLabel,
  proofingStatusTone,
  readableTextOn,
} from "../lib/identity-proofing";
import { Button, Input, Modal, Tag } from "../ui";

const ERROR = "text-error text-[12.5px]";
const NEW_CUSTOMER_FORM = "proofing-new-customer";

const MARK_SIZES = {
  md: "h-8 w-8 text-[13px] rounded-md",
  lg: "h-12 w-12 text-[18px] rounded-lg",
} as const;

// The tile colours a customer without its own gets, picked by name so a
// customer keeps its colour across pages.
const MARK_PALETTE = ["#1F5B4A", "#26307A", "#8B2332", "#3F3F46"] as const;
const MARK_HASH_MULTIPLIER = 31;

function markColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = (hash * MARK_HASH_MULTIPLIER + name.charCodeAt(i)) | 0;
  }
  return MARK_PALETTE[Math.abs(hash) % MARK_PALETTE.length];
}

// A customer's mark: its logo, else its initial on its primary colour (or a
// colour picked by its name).
export function CustomerMark({
  customer,
  size = "md",
}: {
  customer: Pick<ProofingCustomer, "name" | "branding">;
  size?: keyof typeof MARK_SIZES;
}): React.JSX.Element {
  const { branding } = customer;
  const name = branding.displayName || customer.name;
  const [failed, setFailed] = useState(false);
  if (branding.logoUri && !failed) {
    return (
      <img
        src={absoluteApiUrl(branding.logoUri)}
        alt=""
        onError={() => setFailed(true)}
        className={`bg-surface-3 shrink-0 object-contain ${MARK_SIZES[size]}`}
      />
    );
  }
  return (
    <InitialMark
      name={name}
      color={
        isHexColor(branding.primaryColor)
          ? branding.primaryColor
          : markColor(customer.name)
      }
      className={MARK_SIZES[size]}
    />
  );
}

export function InitialMark({
  name,
  color,
  className,
}: {
  name: string;
  color: string;
  className: string;
}): React.JSX.Element {
  return (
    <span
      aria-hidden="true"
      className={`font-display inline-flex shrink-0 items-center justify-center font-bold ${className}`}
      style={{ backgroundColor: color, color: readableTextOn(color) }}
    >
      {name.charAt(0).toUpperCase()}
    </span>
  );
}

// A session's result; a failed one names why, which is what a member acts on.
// compact leaves the reason to the tooltip, for a narrow column.
export function ResultTag({
  request,
  compact = false,
}: {
  request: Pick<ProofingRequest, "status" | "errorCode">;
  compact?: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const label = proofingStatusLabel(request.status, t);
  const reason =
    request.status === "rejected" && request.errorCode
      ? proofingRejectionReason(request.errorCode, t)
      : undefined;
  return (
    <Tag tone={proofingStatusTone(request.status)} dot title={reason}>
      {reason && !compact
        ? t("customers.sessions.failedBecause", {
            status: label,
            reason,
          })
        : label}
    </Tag>
  );
}

export function CustomerStatusTag({
  customer,
}: {
  customer: Pick<ProofingCustomer, "status" | "webhook" | "hasApiKey">;
}): React.JSX.Element {
  const { t } = useTranslation();
  switch (customerDisplayStatus(customer)) {
    case "paused":
      return <Tag dot>{t("customers.status.paused")}</Tag>;
    case "setup_needed":
      return (
        <Tag tone="amber" dot>
          {t("customers.status.setupNeeded")}
        </Tag>
      );
    case "needs_attention":
      return (
        <Tag tone="amber" dot>
          {t("customers.status.needsAttention")}
        </Tag>
      );
    default:
      return (
        <Tag tone="green" dot>
          {t("customers.status.active")}
        </Tag>
      );
  }
}

// How the customer's webhook endpoint answers, in a few words.
export function WebhookStateText({
  webhook,
}: {
  webhook: ProofingCustomer["webhook"];
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const since = webhook.failingSince ? formatWhen(webhook.failingSince) : "—";
  switch (webhook.state) {
    case "delivering":
      return (
        <span>
          {webhook.lastStatusCode === undefined
            ? t("customers.webhookState.delivering")
            : t("customers.webhookState.deliveringCode", {
                code: webhook.lastStatusCode,
              })}
        </span>
      );
    case "failing":
      return (
        <span className="text-error">
          {webhook.lastStatusCode === undefined
            ? t("customers.webhookState.failingNoAnswer", { since })
            : t("customers.webhookState.failing", {
                code: webhook.lastStatusCode,
                since,
              })}
        </span>
      );
    default:
      return (
        <span className="text-muted">
          {t("customers.webhookState.notConfigured")}
        </span>
      );
  }
}

// A secret shown the one time it is readable, with a copy button.
export function SecretReveal({
  title,
  hint,
  secret,
  doneLabel,
  onClose,
}: {
  title: string;
  hint: string;
  secret: string;
  // The close button's label; "I have stored it" when absent.
  doneLabel?: string;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  return (
    <Modal
      title={title}
      closeLabel={t("common.close")}
      onClose={onClose}
      dismissible={false}
      footer={
        <Button size="sm" onClick={onClose}>
          {doneLabel ?? t("customers.secret.done")}
        </Button>
      }
    >
      <p className="text-ink-soft text-[13px]">{hint}</p>
      <div className="border-line bg-surface-2 mt-3 flex items-center gap-2 rounded-lg border p-3">
        <code className="min-w-0 flex-1 font-mono text-[12.5px] break-all">
          {secret}
        </code>
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            // Inside a promise, so a browser without navigator.clipboard (an
            // insecure context) lands in the failure as well: the secret is
            // shown once, so the admin must know to copy it by hand.
            void Promise.resolve()
              .then(() => navigator.clipboard.writeText(secret))
              .then(() => {
                setCopyFailed(false);
                setCopied(true);
              })
              .catch(() => {
                setCopied(false);
                setCopyFailed(true);
              });
          }}
        >
          {copied ? t("customers.secret.copied") : t("customers.secret.copy")}
        </Button>
      </div>
      {copyFailed && (
        <p role="alert" className="text-error mt-2 text-[12.5px]">
          {t("common.copyFailed")}
        </p>
      )}
    </Modal>
  );
}

// The admin's "Add customer": a name is all a customer needs; its flows are
// assigned on its own page, which the new customer opens on.
export function NewCustomerModal({
  slug,
  onClose,
}: {
  slug: string;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const create = useCreateCustomerMutation(slug);
  const [name, setName] = useState("");
  const [touched, setTouched] = useState(false);
  const missing = name.trim() === "";

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (missing) {
      return;
    }
    create.mutate(name.trim(), {
      onSuccess: (customer) => {
        void navigate(`/${slug}/identity-proofing/customers/${customer.id}`);
      },
    });
  }

  return (
    <Modal
      title={t("customers.new.title")}
      closeLabel={t("common.close")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("customers.new.cancel")}
          </Button>
          <Button
            type="submit"
            form={NEW_CUSTOMER_FORM}
            size="sm"
            icon="add"
            loading={create.isPending}
          >
            {t("customers.new.create")}
          </Button>
        </>
      }
    >
      <form
        id={NEW_CUSTOMER_FORM}
        className="flex flex-col gap-1"
        onSubmit={submit}
        noValidate
      >
        <label
          htmlFor="proofing-customer-name"
          className="text-ink-soft text-[12px] font-semibold"
        >
          {t("customers.new.name")}
        </label>
        <Input
          id="proofing-customer-name"
          value={name}
          placeholder={t("customers.new.namePlaceholder")}
          aria-invalid={touched && missing}
          onChange={(event) => setName(event.target.value)}
        />
        {touched && missing && (
          <p className={ERROR}>{t("customers.new.nameRequired")}</p>
        )}
        {create.isError && (
          <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
        )}
      </form>
    </Modal>
  );
}
