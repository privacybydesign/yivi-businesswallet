import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import * as React from "react";
import { useAttestationClaimQuery } from "../api/attestations.queries";
import { ApiError } from "../api/http";
import { Card, Logo, Outcome } from "../ui";

const QR_SIZE = 240;
const NOT_FOUND_STATUS = 404;
const CLAIMED_STATUS = "claimed";
const OFFERED_STATUS = "offered";

export default function Claim(): React.JSX.Element {
  const { t } = useTranslation();
  const { token } = useParams();
  // Guaranteed by the ":token" route segment this component mounts under.
  const claimToken = token!;

  const claim = useAttestationClaimQuery(claimToken);
  const [qrDataUrl, setQrDataUrl] = useState("");

  const offerUri = claim.data?.offerUri;

  // Render the offer link as a QR once loaded.
  useEffect(() => {
    if (!offerUri) {
      return;
    }
    let cancelled = false;
    void QRCode.toDataURL(offerUri, { margin: 1, width: QR_SIZE })
      .then((url) => {
        if (!cancelled) {
          setQrDataUrl(url);
        }
      })
      .catch(() => {
        // The open-wallet button still works even if QR rendering fails.
      });
    return () => {
      cancelled = true;
    };
  }, [offerUri]);

  const notFound =
    claim.isError &&
    claim.error instanceof ApiError &&
    claim.error.status === NOT_FOUND_STATUS;

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {claim.isPending ? (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("common.loading")}
          </p>
        ) : notFound ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("claim.notFoundTitle")}
            message={t("claim.notFoundHint")}
          />
        ) : claim.isError ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("claim.errorTitle")}
            message={t("claim.errorHint")}
          />
        ) : claim.data.status === CLAIMED_STATUS ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("claim.claimedTitle")}
            message={t("claim.claimedHint")}
          />
        ) : (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {claim.data.credentialName}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("claim.issuedBy", { org: claim.data.organizationName })}
            </p>

            <div className="mt-6 flex flex-col items-center gap-4">
              <p className="text-ink-soft text-center text-[13px]">
                {t("claim.scanHint")}
              </p>
              <div
                className="border-line-strong bg-surface rounded-yivi flex items-center justify-center border"
                style={{ width: QR_SIZE, height: QR_SIZE }}
              >
                {qrDataUrl ? (
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
              {claim.data.txCode && (
                <p className="text-ink text-center text-[13px]">
                  {t("claim.txCode", { code: claim.data.txCode })}
                </p>
              )}
              <a
                href={claim.data.offerUri}
                className="rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold"
              >
                {t("claim.openWallet")}
              </a>
              {claim.data.status === OFFERED_STATUS && (
                <p className="text-muted text-center text-[12px]">
                  {t("claim.waiting")}
                </p>
              )}
            </div>
          </>
        )}
      </Card>
    </div>
  );
}
