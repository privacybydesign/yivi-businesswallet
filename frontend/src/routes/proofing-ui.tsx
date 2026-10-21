import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  ProofingBranding,
  ProofingRequest,
  WebhookHealth,
} from "../api/identity-proofing";
import { absoluteApiUrl } from "../api/http";
import { useWhenFormatter } from "../lib/format-when";
import {
  isHexColor,
  proofingRejectionReason,
  proofingStatusLabel,
  proofingStatusTone,
  readableTextOn,
} from "../lib/identity-proofing";
import { Button, Modal, StatusText } from "../ui";

const MARK_SIZES = {
  md: "h-8 w-8 text-[13px] rounded-md",
  lg: "h-12 w-12 text-[18px] rounded-lg",
} as const;

// The tile colour a brand without its own gets, picked by name so it keeps its
// colour across pages.
const MARK_PALETTE = ["#1F5B4A", "#26307A", "#8B2332", "#3F3F46"] as const;
const MARK_HASH_MULTIPLIER = 31;

// A link that looks like Button's secondary/md: Button renders only <button>.
const OPEN_LINK_BUTTON =
  "rounded-yivi font-display border-line-strong bg-surface text-ink hover:bg-surface-3 mr-auto inline-flex h-9 items-center justify-center border px-3.5 text-[13.5px] font-semibold whitespace-nowrap transition-colors duration-150";

function markColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = (hash * MARK_HASH_MULTIPLIER + name.charCodeAt(i)) | 0;
  }
  return MARK_PALETTE[Math.abs(hash) % MARK_PALETTE.length];
}

// The mark of a name as the branding shows it: its logo, else its initial on
// its primary colour (or a colour picked by the name).
export function BrandMark({
  name: orgName,
  branding,
  size = "md",
}: {
  name: string;
  branding: Pick<ProofingBranding, "displayName" | "primaryColor" | "logoUri">;
  size?: keyof typeof MARK_SIZES;
}): React.JSX.Element {
  const name = branding.displayName || orgName;
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
          : markColor(orgName)
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
export function ResultText({
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
  // A session still under way is plain text: it is neither good nor bad yet.
  const status = proofingStatusTone(request.status);
  return (
    <StatusText tone={status === "blue" ? "default" : status} title={reason}>
      {reason && !compact
        ? t("identityProofing.sessions.failedBecause", {
            status: label,
            reason,
          })
        : label}
    </StatusText>
  );
}

// How the org's webhook endpoint answers, in a few words.
export function WebhookStateText({
  webhook,
}: {
  webhook: WebhookHealth;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const since = webhook.failingSince ? formatWhen(webhook.failingSince) : "—";
  switch (webhook.state) {
    case "delivering":
      return (
        <span>
          {webhook.lastStatusCode === undefined
            ? t("identityProofing.webhookState.delivering")
            : t("identityProofing.webhookState.deliveringCode", {
                code: webhook.lastStatusCode,
              })}
        </span>
      );
    case "failing":
      return (
        <span className="text-error">
          {webhook.lastStatusCode === undefined
            ? t("identityProofing.webhookState.failingNoAnswer", { since })
            : t("identityProofing.webhookState.failing", {
                code: webhook.lastStatusCode,
                since,
              })}
        </span>
      );
    default:
      return (
        <span className="text-muted">
          {t("identityProofing.webhookState.notConfigured")}
        </span>
      );
  }
}

// A secret shown the one time it is readable, with a copy button.
export function SecretReveal({
  title,
  hint,
  secret,
  footnote,
  doneLabel,
  link,
  onClose,
}: {
  title: string;
  hint: string;
  secret: string;
  // Shown under the secret, for what to do when it is lost.
  footnote?: string;
  // The close button's label; "I have stored it" when absent.
  doneLabel?: string;
  // A secret that is a link: kept on one line, and opened in a new tab from
  // beside the close button.
  link?: { openLabel: string; newTabNote: string };
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
        <>
          {link && (
            <a
              href={secret}
              target="_blank"
              rel="noopener noreferrer"
              className={OPEN_LINK_BUTTON}
            >
              {link.openLabel}{" "}
              <span className="sr-only">{link.newTabNote}</span>
            </a>
          )}
          <Button onClick={onClose}>
            {doneLabel ?? t("identityProofing.secret.done")}
          </Button>
        </>
      }
    >
      <p className="text-ink-soft text-[13px]">{hint}</p>
      <div className="border-line bg-surface-2 mt-3 flex items-center gap-2 rounded-lg border p-3">
        <code
          className={`min-w-0 flex-1 font-mono text-[12.5px] ${link ? "truncate" : "break-all"}`}
        >
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
          {copied
            ? t("identityProofing.secret.copied")
            : t("identityProofing.secret.copy")}
        </Button>
      </div>
      {copyFailed && (
        <p role="alert" className="text-error mt-2 text-[12.5px]">
          {t("common.copyFailed")}
        </p>
      )}
      {footnote && <p className="text-ink-soft mt-3 text-[13px]">{footnote}</p>}
    </Modal>
  );
}
