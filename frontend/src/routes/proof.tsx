import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import * as React from "react";
import {
  useProofingLinkQuery,
  useStartProofingMutation,
} from "../api/identity-proofing.queries";
import { ApiError } from "../api/http";
import type { ProofingStart } from "../api/identity-proofing";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card, Logo, Outcome } from "../ui";

// The link's expiry as a clock time in the reader's language: it is minutes away.
function useTimeFormatter(): (iso: string) => string {
  const { i18n } = useTranslation();
  return React.useCallback(
    (iso: string) =>
      new Date(iso).toLocaleTimeString(i18n.language, {
        hour: "2-digit",
        minute: "2-digit",
      }),
    [i18n.language],
  );
}

const QR_SIZE = 240;
const NOT_FOUND_STATUS = 404;

// The public page an identity proofing e-mail links to (from its QR code and its
// button alike). The recipient needs no account: the link token is the key, and
// the link lives exactly as long as the proofing session it was sent with. The
// page shows the vcmrtd/idem QR code and the open-in-app deep link, which are
// one payload, so both always do the same. The claim inside it is single-use and
// shorter-lived than the session, so a fresh one is fetched when it lapses,
// until the phone has picked the session up.
export default function Proof(): React.JSX.Element {
  const { t } = useTranslation();
  const { token } = useParams();
  // Guaranteed by the ":token" route segment this component mounts under.
  const proofToken = token!;

  const start = useStartProofingMutation(proofToken);
  // The last start answer, kept across a refresh: a mutation's own data is
  // cleared while the next call is pending, which would flash the spinner.
  const [current, setCurrent] = useState<ProofingStart | null>(null);
  const link = useProofingLinkQuery(proofToken, current !== null);
  const formatTime = useTimeFormatter();
  // The rendered QR, keyed by the link it encodes so a stale one never shows.
  const [qr, setQr] = useState<{ link: string; dataUrl: string } | null>(null);

  const deepLink = current?.deepLink;
  const claimExpiresAt = current?.claimExpiresAt;
  const qrDataUrl = qr !== null && qr.link === deepLink ? qr.dataUrl : "";

  // Render the vcmrtd link as a QR.
  useEffect(() => {
    if (!deepLink) {
      return;
    }
    let cancelled = false;
    void QRCode.toDataURL(deepLink, { margin: 1, width: QR_SIZE })
      .then((dataUrl) => {
        if (!cancelled) {
          setQr({ link: deepLink, dataUrl });
        }
      })
      .catch(() => {
        // The open-in-app link still works even if QR rendering fails.
      });
    return () => {
      cancelled = true;
    };
  }, [deepLink]);

  // Ask for a fresh QR when this one lapses. Once the phone has claimed the
  // session the service hands out no new one, and the page says to continue there.
  const { mutate } = start;
  const begin = React.useCallback(
    () => mutate(undefined, { onSuccess: setCurrent }),
    [mutate],
  );
  const status = link.data?.status;
  const waiting = status === "pending" || status === "in_progress";
  // The session already exists (it was created when the mail was sent), so the
  // QR is fetched as soon as the page knows the link is live.
  const asked = current !== null || start.isPending || start.isError;
  useEffect(() => {
    if (waiting && !asked) {
      begin();
    }
  }, [waiting, asked, begin]);
  useEffect(() => {
    if (!claimExpiresAt || !waiting) {
      return;
    }
    const delay = Math.max(0, new Date(claimExpiresAt).getTime() - Date.now());
    const timer = window.setTimeout(begin, delay);
    return () => window.clearTimeout(timer);
  }, [claimExpiresAt, waiting, begin]);

  const org = link.data?.organizationName ?? "";
  const notFound =
    link.isError &&
    link.error instanceof ApiError &&
    link.error.status === NOT_FOUND_STATUS;

  function body(): React.ReactNode {
    if (link.isPending) {
      return (
        <p className="text-ink-soft mt-6 text-center text-[14px]">
          {t("common.loading")}
        </p>
      );
    }
    if (notFound) {
      return (
        <Outcome
          tone="error"
          icon="warning"
          title={t("identityProofing.proof.notFoundTitle")}
          message={t("identityProofing.proof.notFoundHint")}
        />
      );
    }
    if (link.isError) {
      return (
        <Outcome
          tone="error"
          icon="warning"
          title={t("identityProofing.proof.errorTitle")}
          message={t("identityProofing.proof.errorHint")}
        />
      );
    }
    switch (link.data.status) {
      case "approved":
        return (
          <Outcome
            tone="success"
            icon="valid"
            title={t("identityProofing.proof.approvedTitle")}
            message={t("identityProofing.proof.approvedHint", { org })}
          />
        );
      case "rejected":
        return (
          <Outcome
            tone="error"
            icon="warning"
            title={t("identityProofing.proof.rejectedTitle")}
            message={t("identityProofing.proof.rejectedHint", { org })}
          />
        );
      case "needs_review":
        return (
          <Outcome
            tone="info"
            icon="time"
            title={t("identityProofing.proof.reviewTitle")}
            message={t("identityProofing.proof.reviewHint", { org })}
          />
        );
      case "expired":
        return (
          <Outcome
            tone="error"
            icon="time"
            title={t("identityProofing.proof.expiredTitle")}
            message={t("identityProofing.proof.expiredHint", { org })}
          />
        );
      default:
        return (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {t("identityProofing.proof.title")}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("identityProofing.proof.requestedBy", { org })}
            </p>
            <p className="text-ink-soft mt-4 text-center text-[13px]">
              {t("identityProofing.proof.steps")}
            </p>
            <p className="text-muted mt-2 text-center text-[12px]">
              {t("identityProofing.proof.validUntil", {
                time: formatTime(link.data.linkExpiresAt),
              })}
            </p>
            <div className="mt-6 flex flex-col items-center gap-4">
              {!current ? (
                start.isError ? (
                  <Button size="lg" onClick={begin}>
                    {t("identityProofing.proof.retry")}
                  </Button>
                ) : (
                  <span
                    aria-hidden="true"
                    className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
                  />
                )
              ) : deepLink ? (
                <>
                  <p className="text-ink-soft text-center text-[13px]">
                    {t("identityProofing.proof.scanHint")}
                  </p>
                  <div
                    className="border-line-strong bg-surface rounded-yivi flex items-center justify-center border"
                    style={{ width: QR_SIZE, height: QR_SIZE }}
                  >
                    {qrDataUrl && !start.isPending ? (
                      <img
                        src={qrDataUrl}
                        alt=""
                        width={QR_SIZE}
                        height={QR_SIZE}
                        className="rounded-yivi"
                      />
                    ) : (
                      <span
                        aria-hidden="true"
                        className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
                      />
                    )}
                  </div>
                  <a
                    href={deepLink}
                    className="rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold"
                  >
                    {t("identityProofing.proof.openApp")}
                  </a>
                  <p className="text-muted text-center text-[12px]">
                    {start.isPending
                      ? t("identityProofing.proof.refreshing")
                      : t("identityProofing.proof.waiting")}
                  </p>
                </>
              ) : (
                <p className="text-ink text-center text-[14px]">
                  {t("identityProofing.proof.continueInApp")}
                </p>
              )}
              {start.isError && (
                <p className="text-error text-center text-[12.5px]">
                  {proofingErrorMessage(start.error, t)}
                </p>
              )}
            </div>
          </>
        );
    }
  }

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        {body()}
      </Card>
    </div>
  );
}
