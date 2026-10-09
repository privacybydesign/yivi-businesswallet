import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  useDeleteHeldAttestationMutation,
  useHeldAttestationClaimsQuery,
  useHeldHistoryQuery,
  useRecheckHeldMutation,
} from "../api/attestations.queries";
import type { HeldAttestationClaims } from "../api/attestations";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import { credentialDisplayName } from "../lib/credential-display";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import {
  HELD_STATUS_TONES,
  heldExpiryAt,
  heldExpiryIsPast,
  heldFormatLabel,
  heldHistory,
  heldSourceLabel,
  heldStatus,
  heldStatusLabel,
} from "../lib/held-credential";
import type { HeldHistoryEntry, HeldStatus } from "../lib/held-credential";
import { Button, Card, ConfirmDialog, Icon, Tag, TopBar } from "../ui";
import type { IconName } from "../ui";

const NOT_FOUND_STATUS = 404;
const ADMIN_ROLE = "admin";
const BANNER_ICON_SIZE = 22;
const CHECK_ICON_SIZE = 16;
const NOTE_ICON_SIZE = 14;
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
const CARD_TITLE = "font-display text-ink text-[17px] font-bold";

// formatClaimValue renders a disclosed SD-JWT claim value for display. Primitives
// show as text; objects/arrays are JSON-stringified so nested claims stay legible.
function formatClaimValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "—";
  }
  if (typeof value === "string") {
    return value;
  }
  if (
    typeof value === "number" ||
    typeof value === "boolean" ||
    typeof value === "bigint"
  ) {
    return String(value);
  }
  return JSON.stringify(value);
}

// The detail page for one credential the organization holds: its state and
// what to do about it, the attributes it discloses, its history, where it came
// from, and what the wallet checked about it.
export default function AttestationHeldDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, heldId } = useParams();
  // Both are guaranteed by the ":orgSlug/attestations/held/:heldId" route.
  const slug = orgSlug!;
  const id = heldId!;
  const navigate = useNavigate();

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === ADMIN_ROLE;
  const claims = useHeldAttestationClaimsQuery(slug, id, !org.isError);
  const remove = useDeleteHeldAttestationMutation(slug);
  const [confirmingRemove, setConfirmingRemove] = useState(false);
  const formatWhen = useWhenFormatter();
  const formatDate = useDateFormatter();

  const credential = claims.data;
  const name = credential
    ? credential.displayName || credentialDisplayName(credential.vct)
    : t("attestations.held.detail.title");

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={name} />
      <div className="p-4 sm:p-8">{body}</div>
    </>
  );
  const message = (text: string, isError = false): React.JSX.Element => (
    <Card className="p-6">
      <p className={`text-[14px] ${isError ? "text-error" : "text-ink-soft"}`}>
        {text}
      </p>
    </Card>
  );

  if (org.isError) {
    return shell(message(accessMessage(org.error, t), true));
  }
  if (
    claims.error instanceof ApiError &&
    claims.error.status === NOT_FOUND_STATUS
  ) {
    return shell(message(t("attestations.held.detail.notFound")));
  }
  if (claims.isError) {
    return shell(
      message(
        t("attestations.loadError", { message: claims.error.message }),
        true,
      ),
    );
  }
  if (!credential) {
    return shell(message(t("common.loading")));
  }

  const now = new Date();
  const status = heldStatus(credential, now);
  const issuer = credential.issuerName || credential.issuer;

  return (
    <>
      <TopBar
        title={name}
        badges={
          <Tag tone={HELD_STATUS_TONES[status]} dot>
            {heldStatusLabel(status, t)}
          </Tag>
        }
        subtitle={t("attestations.held.detail.byline", {
          issuer,
          received: formatWhen(credential.receivedAt),
          source: heldSourceLabel(credential.source, t),
        })}
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => exportCredential(credential, name)}
            >
              {t("attestations.held.detail.export")}
            </Button>
            {isAdmin && (
              <Button
                variant="dangerGhost"
                icon="delete"
                className="border-error border"
                onClick={() => setConfirmingRemove(true)}
              >
                {t("attestations.held.delete")}
              </Button>
            )}
          </>
        }
      />
      {confirmingRemove && (
        <ConfirmDialog
          title={t("attestations.held.delete")}
          message={t("attestations.held.confirmDelete", { name })}
          confirmLabel={t("attestations.held.delete")}
          busy={remove.isPending}
          onConfirm={() =>
            remove.mutate(
              { heldId: credential.id },
              {
                onSuccess: () =>
                  void navigate(`/${slug}/attestations?tab=held`, {
                    replace: true,
                  }),
              },
            )
          }
          onClose={() => setConfirmingRemove(false)}
        />
      )}

      <div className="flex flex-col gap-5 p-4 sm:p-8">
        <StatusBanner
          slug={slug}
          credential={credential}
          status={status}
          isAdmin={isAdmin}
          now={now}
          formatDate={formatDate}
        />
        <div className="grid grid-cols-1 items-start gap-5 lg:grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)]">
          <div className="flex flex-col gap-5">
            <AttributesCard credential={credential} />
            <HistoryCard slug={slug} credential={credential} />
          </div>
          <div className="flex flex-col gap-5">
            <ProvenanceCard
              credential={credential}
              orgName={org.data?.name ?? ""}
              status={status}
              formatDate={formatDate}
              formatWhen={formatWhen}
            />
            <ChecksCard
              credential={credential}
              status={status}
              now={now}
              formatDate={formatDate}
              formatWhen={formatWhen}
            />
          </div>
        </div>
      </div>
    </>
  );
}

// Saves what the wallet shows of the credential as a JSON file: provenance,
// validity and the attribute values. Not the signed credential itself, which
// stays in the wallet.
function exportCredential(
  credential: HeldAttestationClaims,
  name: string,
): void {
  const body = {
    id: credential.id,
    name,
    type: credential.vct,
    format: credential.format,
    issuer: credential.issuer,
    issuerName: credential.issuerName,
    source: credential.source,
    receivedAt: credential.receivedAt,
    issuedAt: credential.issuedAt,
    expiresAt: credential.expiresAt,
    revoked: credential.revoked,
    statusCheckedAt: credential.statusCheckedAt,
    attributes: Object.fromEntries(
      credential.attributes.map((a) => [a.key, a.value]),
    ),
  };
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(body, null, 2)], { type: "application/json" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, "-").toLowerCase() || "credential"}.json`;
  link.click();
  URL.revokeObjectURL(url);
}

const BANNER_LOOK: Record<
  HeldStatus,
  { box: string; icon: IconName; iconClass: string }
> = {
  revoked: {
    box: "bg-error-bg border-error/30",
    icon: "invalid",
    iconClass: "text-ink",
  },
  expired: {
    box: "bg-error-bg border-error/30",
    icon: "time",
    iconClass: "text-ink",
  },
  expiringSoon: {
    box: "bg-warning-bg border-warning/40",
    icon: "warning",
    iconClass: "text-ink",
  },
  valid: {
    box: "bg-highlight border-link/20",
    icon: "valid",
    iconClass: "text-ink",
  },
};

// The credential's state in words, and what the wallet can do about it now:
// re-read the issuer's status list.
function StatusBanner({
  slug,
  credential,
  status,
  isAdmin,
  now,
  formatDate,
}: {
  slug: string;
  credential: HeldAttestationClaims;
  status: HeldStatus;
  isAdmin: boolean;
  now: Date;
  formatDate: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const recheck = useRecheckHeldMutation(slug, credential.id);
  const look = BANNER_LOOK[status];
  const expiry = credential.expiresAt ? formatDate(credential.expiresAt) : "";
  const { title, body } = bannerText(
    credential,
    status,
    expiry,
    now,
    formatDate,
    t,
  );

  return (
    <div
      className={`flex flex-wrap items-center gap-4 rounded-lg border px-5 py-4 ${look.box}`}
    >
      <span aria-hidden="true" className={look.iconClass}>
        <Icon name={look.icon} size={BANNER_ICON_SIZE} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-ink text-[15px] font-bold">{title}</div>
        <p className="text-ink-soft mt-0.5 max-w-3xl text-[13px]">{body}</p>
        {recheck.isError && (
          <p className="text-error mt-1 text-[12.5px]">
            {t("attestations.loadError", { message: recheck.error.message })}
          </p>
        )}
      </div>
      {isAdmin && credential.hasStatusList && (
        <Button
          variant="secondary"
          loading={recheck.isPending}
          onClick={() => recheck.mutate()}
        >
          {t("attestations.held.detail.recheck")}
        </Button>
      )}
    </div>
  );
}

function bannerText(
  credential: HeldAttestationClaims,
  status: HeldStatus,
  expiry: string,
  now: Date,
  formatDate: (iso: string) => string,
  t: TFunction,
): { title: string; body: string } {
  switch (status) {
    case "revoked":
      return {
        title: credential.statusCheckedAt
          ? t("attestations.held.banner.revokedChecked", {
              date: formatDate(credential.statusCheckedAt),
            })
          : t("attestations.held.banner.revoked"),
        body: t("attestations.held.banner.revokedBody"),
      };
    case "expired":
      return {
        title: t("attestations.held.banner.expired", { date: expiry }),
        body: t("attestations.held.banner.expiredBody"),
      };
    case "expiringSoon":
      return {
        title: t("attestations.held.banner.expiring", { date: expiry }),
        body: t("attestations.held.banner.expiringBody"),
      };
    case "valid":
      return {
        title: expiry
          ? t("attestations.held.banner.valid", { date: expiry })
          : t("attestations.held.banner.validForever"),
        body: heldExpiryIsPast(credential, now)
          ? ""
          : credential.hasStatusList
            ? t("attestations.held.banner.validBody")
            : t("attestations.held.banner.validNoStatusBody"),
      };
  }
}

function CardHeader({
  title,
  meta,
}: {
  title: string;
  meta?: string;
}): React.JSX.Element {
  return (
    <div className="border-line flex items-baseline gap-2 border-b px-5 py-4">
      <h2 className={CARD_TITLE}>{title}</h2>
      {meta && <span className="text-muted text-[12.5px]">{meta}</span>}
    </div>
  );
}

function AttributesCard({
  credential,
}: {
  credential: HeldAttestationClaims;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Card>
      <CardHeader
        title={t("attestations.held.detail.attributes")}
        meta={t("attestations.held.detail.attributeCount", {
          count: credential.attributes.length,
        })}
      />
      {credential.attributes.length === 0 ? (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("attestations.held.detail.noAttributes")}
        </p>
      ) : (
        <dl className="divide-line divide-y">
          {credential.attributes.map((attribute) => (
            <div
              key={attribute.key}
              className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)] gap-4 px-5 py-3.5"
            >
              <dt>
                <div className="text-ink text-[13.5px] font-semibold">
                  {attribute.label || attribute.key}
                </div>
                {attribute.label && (
                  <div className="text-muted font-mono text-[11.5px]">
                    {attribute.key}
                  </div>
                )}
              </dt>
              <dd className="text-ink text-[13.5px] break-words">
                {formatClaimValue(attribute.value)}
              </dd>
            </div>
          ))}
        </dl>
      )}
      <p className="border-line text-muted flex items-start gap-2 border-t px-5 py-3 text-[12px]">
        <span className="mt-0.5 shrink-0" aria-hidden="true">
          <Icon name="info" size={NOTE_ICON_SIZE} />
        </span>
        {t("attestations.held.detail.attributesNote")}
      </p>
    </Card>
  );
}

function HistoryCard({
  slug,
  credential,
}: {
  slug: string;
  credential: HeldAttestationClaims;
}): React.JSX.Element {
  const { t } = useTranslation();
  const history = useHeldHistoryQuery(slug, credential.id);
  const formatWhen = useWhenFormatter();
  const entries = history.data ? heldHistory(history.data, credential) : [];

  return (
    <Card>
      <CardHeader title={t("attestations.held.detail.history")} />
      {history.isPending ? (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {t("common.loading")}
        </p>
      ) : history.isError ? (
        <p className="text-error px-5 py-4 text-[13px]">
          {t("attestations.loadError", { message: history.error.message })}
        </p>
      ) : (
        <ol className="divide-line divide-y">
          {entries.map((entry, index) => {
            const { title, detail } = historyText(entry, credential, t);
            return (
              <li
                // The trail is positional; two entries can share a time.
                key={`${entry.at}-${index}`}
                className="grid grid-cols-[150px_16px_1fr] items-start gap-3 px-5 py-3.5"
              >
                <time dateTime={entry.at} className="text-muted text-[12.5px]">
                  {formatWhen(entry.at)}
                </time>
                <span
                  aria-hidden="true"
                  className="bg-line-strong mt-1.5 h-1.5 w-1.5 justify-self-center rounded-full"
                />
                <div>
                  <div className="text-ink text-[13.5px] font-semibold">
                    {title}
                  </div>
                  {detail && (
                    <div className="text-muted text-[12.5px]">{detail}</div>
                  )}
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </Card>
  );
}

function historyText(
  entry: HeldHistoryEntry,
  credential: HeldAttestationClaims,
  t: TFunction,
): { title: string; detail: string } {
  switch (entry.kind) {
    case "received": {
      const via = t("attestations.held.history.via", {
        source: heldSourceLabel(credential.source, t),
      });
      const parts: string[] = [via];
      if (entry.sender) {
        parts.push(
          t("attestations.held.history.from", { sender: entry.sender }),
        );
      }
      if (entry.actor) {
        parts.push(
          t("attestations.held.history.acceptedBy", { name: entry.actor }),
        );
      }
      return {
        title: t("attestations.held.history.received"),
        detail: parts.join(", "),
      };
    }
    case "statusChanged":
      if (entry.revoked === undefined) {
        return {
          title: t("attestations.held.history.statusChanged"),
          detail: t("attestations.held.history.detailHidden"),
        };
      }
      return entry.revoked
        ? {
            title: t("attestations.held.history.revoked"),
            detail: t("attestations.held.history.revokedDetail"),
          }
        : {
            title: t("attestations.held.history.reinstated"),
            detail: t("attestations.held.history.reinstatedDetail"),
          };
    case "statusChecked":
      return {
        title: t("attestations.held.history.checked"),
        detail: credential.revoked
          ? t("attestations.held.history.checkedRevoked")
          : t("attestations.held.history.checkedValid"),
      };
    case "removed":
      return {
        title: t("attestations.held.history.removed"),
        detail: entry.actor ?? "",
      };
    default:
      return { title: entry.action, detail: entry.actor ?? "" };
  }
}

function ProvenanceCard({
  credential,
  orgName,
  status,
  formatDate,
  formatWhen,
}: {
  credential: HeldAttestationClaims;
  orgName: string;
  status: HeldStatus;
  formatDate: (iso: string) => string;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const expiresAt =
    heldExpiryAt(credential) === null ? undefined : credential.expiresAt;
  const validity = [
    credential.issuedAt ? formatDate(credential.issuedAt) : "",
    expiresAt
      ? formatDate(expiresAt)
      : t("attestations.held.detail.doesNotExpire"),
  ]
    .filter(Boolean)
    // A range takes an en dash, not an em dash.
    .join(" – ");
  const rows: [string, string, boolean?][] = [
    [
      t("attestations.held.fields.issuer"),
      credential.issuerName || credential.issuer,
    ],
    [t("attestations.held.detail.issuerEndpoint"), credential.issuer, true],
    [t("attestations.held.detail.type"), credential.vct, true],
    [t("attestations.held.detail.format"), heldFormatLabel(credential.format)],
    [
      t("attestations.held.fields.received"),
      t("attestations.held.detail.receivedVia", {
        date: formatWhen(credential.receivedAt),
        source: heldSourceLabel(credential.source, t),
      }),
    ],
    [
      t("attestations.held.detail.validity"),
      status === "revoked"
        ? t("attestations.held.detail.validityRevoked", { validity })
        : validity,
    ],
    [
      t("attestations.held.detail.heldBy"),
      t("attestations.held.detail.heldByValue", { org: orgName }),
    ],
  ];
  return (
    <Card>
      <CardHeader title={t("attestations.held.detail.provenance")} />
      <dl className="divide-line divide-y px-5">
        {rows.map(([label, value, mono]) => (
          <div key={label} className="py-3">
            <dt className={CAPTION}>{label}</dt>
            <dd
              className={`text-ink mt-1 break-all ${mono ? "font-mono text-[12px]" : "text-[13.5px]"}`}
            >
              {value}
            </dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

type CheckState = "ok" | "fail" | "neutral";

const CHECK_LOOK: Record<CheckState, { icon: IconName; label: string }> = {
  ok: { icon: "valid", label: "text-ink" },
  fail: { icon: "invalid", label: "text-error font-semibold" },
  neutral: { icon: "info", label: "text-ink-soft" },
};

// What the wallet verified about the credential: its signature and issuer at
// receipt (a credential failing either is never stored), its status list, and
// its validity period.
function ChecksCard({
  credential,
  status,
  now,
  formatDate,
  formatWhen,
}: {
  credential: HeldAttestationClaims;
  status: HeldStatus;
  now: Date;
  formatDate: (iso: string) => string;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  // A seeded demo credential never went through the receive-time checks.
  const verified = credential.source !== "bootstrap";
  const receipt = formatDate(credential.receivedAt);
  const checks: { state: CheckState; label: string; detail: string }[] = [
    verified
      ? {
          state: "ok",
          label: t("attestations.held.checks.signature"),
          detail: t("attestations.held.checks.atReceipt", { date: receipt }),
        }
      : {
          state: "neutral",
          label: t("attestations.held.checks.seeded"),
          detail: "",
        },
    ...(verified
      ? [
          {
            state: "ok" as const,
            label: t("attestations.held.checks.issuerTrusted"),
            detail: t("attestations.held.checks.atReceipt", { date: receipt }),
          },
        ]
      : []),
    !credential.hasStatusList
      ? {
          state: "neutral",
          label: t("attestations.held.checks.noStatusList"),
          detail: "",
        }
      : status === "revoked"
        ? {
            state: "fail",
            label: t("attestations.held.checks.revoked"),
            detail: credential.statusCheckedAt
              ? t("attestations.held.checks.checked", {
                  when: formatWhen(credential.statusCheckedAt),
                })
              : "",
          }
        : {
            state: "ok",
            label: t("attestations.held.checks.notRevoked"),
            detail: credential.statusCheckedAt
              ? t("attestations.held.checks.checked", {
                  when: formatWhen(credential.statusCheckedAt),
                })
              : "",
          },
    heldExpiryIsPast(credential, now)
      ? {
          state: "fail",
          label: t("attestations.held.checks.outsideValidity"),
          detail: credential.expiresAt
            ? t("attestations.held.checks.expired", {
                date: formatDate(credential.expiresAt),
              })
            : "",
        }
      : {
          state: "ok",
          label: t("attestations.held.checks.withinValidity"),
          detail: "",
        },
  ];

  return (
    <Card>
      <CardHeader
        title={t("attestations.held.detail.checks")}
        meta={
          credential.statusCheckedAt
            ? t("attestations.held.checks.lastChecked", {
                when: formatWhen(credential.statusCheckedAt),
              })
            : undefined
        }
      />
      <ul className="flex flex-col gap-3.5 px-5 py-4">
        {checks.map((check) => {
          const look = CHECK_LOOK[check.state];
          return (
            <li
              key={check.label}
              className="flex items-start justify-between gap-4 text-[13px]"
            >
              <span className={`flex items-start gap-2.5 ${look.label}`}>
                <span className="mt-px shrink-0" aria-hidden="true">
                  <Icon name={look.icon} size={CHECK_ICON_SIZE} />
                </span>
                {check.label}
              </span>
              {check.detail && (
                <span className="text-muted shrink-0 text-right text-[12.5px]">
                  {check.detail}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </Card>
  );
}
