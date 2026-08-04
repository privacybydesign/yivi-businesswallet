import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useHeldAttestationClaimsQuery } from "../api/attestations.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import { credentialDisplayName } from "../lib/credential-display";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import {
  HELD_STATUS_TONES,
  heldExpiryAt,
  heldExpiryIsPast,
  heldSourceLabel,
  heldStatus,
  heldStatusLabel,
} from "../lib/held-credential";
import { Card, Tag, TopBar } from "../ui";

const NOT_FOUND_STATUS = 404;

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

function DetailRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
}): React.JSX.Element {
  return (
    <>
      <dt className="text-ink-soft text-[12.5px]">{label}</dt>
      <dd
        className={
          mono
            ? "text-ink-soft font-mono text-[12px] break-all"
            : "text-ink text-[13px]"
        }
      >
        {value}
      </dd>
    </>
  );
}

// The detail page for one credential the organization holds: its provenance and
// validity, plus the attributes it discloses (read from the holder engine on open).
// Attribute values render generically since an SD-JWT payload may carry any JSON
// type.
export default function AttestationHeldDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, heldId } = useParams();
  // Both are guaranteed by the ":orgSlug/attestations/held/:heldId" route.
  const slug = orgSlug!;
  const id = heldId!;

  const org = useOrganizationQuery(slug);
  const claims = useHeldAttestationClaimsQuery(slug, id, !org.isError);
  const formatWhen = useWhenFormatter();
  const formatDate = useDateFormatter();

  const credential = claims.data;
  const name = credential
    ? credential.displayName || credentialDisplayName(credential.vct)
    : t("attestations.held.detail.title");

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={name} />
      <div className="p-8">{body}</div>
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
  // Same rule the card follows: an expiry that does not parse is not a date, so it
  // reads as "does not expire" rather than being echoed back as one.
  const expiresAt =
    heldExpiryAt(credential) === null ? undefined : credential.expiresAt;

  return (
    <>
      <TopBar
        title={name}
        subtitle={credential.issuerName || credential.issuer}
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]">
        <Card className="p-6">
          <h2 className="text-[16px] font-semibold">
            {t("attestations.held.detail.attributes")}
          </h2>
          {credential.attributes.length === 0 ? (
            <p className="text-ink-soft mt-2 text-[14px]">
              {t("attestations.held.detail.noAttributes")}
            </p>
          ) : (
            <dl className="border-line divide-line mt-4 divide-y rounded-md border">
              {credential.attributes.map((attribute) => (
                <div
                  key={attribute.key}
                  className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-4 px-3 py-2"
                >
                  <dt className="text-ink-soft truncate text-[12.5px]">
                    {attribute.label || attribute.key}
                  </dt>
                  <dd className="text-ink text-[13px] break-words">
                    {formatClaimValue(attribute.value)}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </Card>

        <Card className="h-fit p-0">
          <div className="border-line flex flex-col items-center gap-3 border-b p-6">
            {credential.logoUri && (
              <img
                src={credential.logoUri}
                alt={t("attestations.credentialImageAlt")}
                className="border-line bg-surface h-16 w-16 shrink-0 rounded-md border object-contain"
              />
            )}
            <div className="text-center">
              <div className="font-display text-[18px] font-bold">{name}</div>
            </div>
            <Tag tone={HELD_STATUS_TONES[status]} dot>
              {heldStatusLabel(status, t)}
            </Tag>
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2.5 p-5">
            <DetailRow
              label={t("attestations.held.fields.issuer")}
              value={credential.issuerName || credential.issuer}
            />
            <DetailRow
              label={t("attestations.held.fields.source")}
              value={heldSourceLabel(credential.source, t)}
            />
            <DetailRow
              label={t("attestations.held.fields.received")}
              value={formatWhen(credential.receivedAt)}
            />
            <DetailRow
              label={
                heldExpiryIsPast(credential, now)
                  ? t("attestations.held.fields.expired")
                  : t("attestations.held.fields.expires")
              }
              value={
                expiresAt
                  ? formatDate(expiresAt)
                  : t("attestations.held.detail.doesNotExpire")
              }
            />
            <DetailRow
              label={t("attestations.held.detail.type")}
              value={credential.vct}
              mono
            />
          </dl>
        </Card>
      </div>
    </>
  );
}
