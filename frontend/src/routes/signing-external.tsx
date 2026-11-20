import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import {
  SIGNER_STATUS,
  SIGNING_STATUS,
  externalDocumentUrl,
  getExternalDocument,
} from "../api/signing";
import {
  useDeclineExternalSignMutation,
  useExternalSigningQuery,
  useLinkExternalCredentialMutation,
  useStartExternalSignMutation,
} from "../api/signing.queries";
import { returnLinkHost } from "../lib/return-link";
import { modeLabel } from "../lib/signing-labels";
import { toast } from "../lib/toast";
import { Button, Card, Logo, Outcome, buttonClassName } from "../ui";
import { DeclineDialog } from "./signing-decline-dialog";
import { SigningDocumentViewer } from "./signing-document-viewer";

// The external signing page. An external signee was added to a co-signing request by
// name + e-mail, so they have no account here and no session: this page is reached
// with nothing but the one-time link from their mail, and every call it makes is keyed
// by that token. They run the same two ceremonies a member does — link a signing
// certificate by identifying with their own wallet app, then sign — which is what
// makes the finished PDF carry one signature per signer regardless of who is inside
// the org. A signee sent here by another site (a request made over the signing API)
// is offered a link back to it once they are done; they are never sent there unasked.
// While the signee still has to sign, the document itself is on the page beside the
// buttons, so what they sign is what they are looking at.

const NOT_FOUND_STATUS = 404;

// The way back to the site that sent the signee here, for a signee who is done.
// Nothing when there is no such site.
function returnLink(
  returnUrl: string | undefined,
  t: TFunction,
): React.ReactNode {
  const host = returnLinkHost(returnUrl);
  if (host === null) {
    return undefined;
  }
  return (
    <a href={returnUrl} className={buttonClassName("primary")}>
      {t("signing.external.returnTo", { host })}
    </a>
  );
}

export default function SigningExternal(): React.JSX.Element {
  const { t } = useTranslation();
  const { token } = useParams();
  // Guaranteed by the ":token" route segment this component mounts under.
  const signToken = token!;

  const [searchParams, setSearchParams] = useSearchParams();
  const view = useExternalSigningQuery(signToken);
  const link = useLinkExternalCredentialMutation(signToken);
  const sign = useStartExternalSignMutation(signToken);
  const decline = useDeclineExternalSignMutation(signToken);
  const [declining, setDeclining] = useState(false);

  // The ceremony returns here with ?link=ok|failed (credential link) or ?request=<id>
  // (signature). Announce the link outcome once, then strip the flag so a refresh does
  // not re-fire the toast; the signature outcome is visible in the state below.
  const linkResult = searchParams.get("link");
  useEffect(() => {
    if (!linkResult) return;
    if (linkResult === "ok") toast.success(t("signing.external.linkedToast"));
    else toast.error(t("signing.external.linkFailedToast"));
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("link");
        return next;
      },
      { replace: true },
    );
  }, [linkResult, t, setSearchParams]);

  const onLink = (): void =>
    link.mutate(undefined, {
      onSuccess: (start) => window.location.assign(start.authorizeUrl),
      onError: () => toast.error(t("signing.external.startError")),
    });

  const onSign = (): void =>
    sign.mutate(undefined, {
      onSuccess: (start) => window.location.assign(start.authorizeUrl),
      onError: () => toast.error(t("signing.external.startError")),
    });

  const onDecline = (reason: string): void =>
    decline.mutate(reason, {
      onSuccess: () => {
        toast.success(t("signing.decline.toastSuccess"));
        setDeclining(false);
      },
      onError: () => toast.error(t("signing.external.declineError")),
    });

  const invalidLink =
    view.isError &&
    view.error instanceof ApiError &&
    view.error.status === NOT_FOUND_STATUS;

  // The signee still has a decision to make: the only state the document is shown in.
  const reviewing =
    view.isSuccess &&
    view.data.signerStatus !== SIGNER_STATUS.signed &&
    view.data.signerStatus !== SIGNER_STATUS.declined &&
    view.data.status !== SIGNING_STATUS.failed &&
    view.data.status !== SIGNING_STATUS.declined;

  return (
    <div
      className={
        reviewing
          ? "bg-surface-2 flex min-h-screen flex-col items-center gap-6 p-6 lg:flex-row lg:items-start lg:justify-center"
          : "bg-surface-2 flex min-h-screen items-center justify-center p-6"
      }
    >
      {reviewing && (
        <Card className="w-full max-w-3xl min-w-0 p-4 lg:flex-1">
          <SigningDocumentViewer
            filename={view.data.filename}
            load={(signal) => getExternalDocument(signToken, signal)}
          />
        </Card>
      )}
      <Card className="w-full max-w-md p-6 sm:p-8 lg:sticky lg:top-6">
        <div className="flex justify-center">
          <Logo />
        </div>

        {view.isPending ? (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("common.loading")}
          </p>
        ) : invalidLink ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("signing.external.invalidTitle")}
            message={t("signing.external.invalidHint")}
          />
        ) : view.isError ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("signing.external.errorTitle")}
            message={t("signing.external.errorHint")}
          />
        ) : view.data.signerStatus === SIGNER_STATUS.signed ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("signing.external.signedTitle")}
            message={
              view.data.status === SIGNING_STATUS.completed
                ? t("signing.external.signedAllHint")
                : view.data.status === SIGNING_STATUS.declined
                  ? t("signing.external.requestDeclinedHint")
                  : t("signing.external.signedWaitingHint")
            }
            action={returnLink(view.data.returnUrl, t)}
          />
        ) : view.data.signerStatus === SIGNER_STATUS.declined ? (
          <Outcome
            tone="info"
            icon="close"
            title={t("signing.external.declinedTitle")}
            message={t("signing.external.declinedHint")}
            action={returnLink(view.data.returnUrl, t)}
          />
        ) : view.data.status === SIGNING_STATUS.failed ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("signing.external.failedTitle")}
            message={t("signing.external.failedHint")}
          />
        ) : view.data.status === SIGNING_STATUS.declined ? (
          <Outcome
            tone="info"
            icon="warning"
            title={t("signing.external.requestDeclinedTitle")}
            message={t("signing.external.requestDeclinedHint")}
          />
        ) : (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold wrap-anywhere">
              {view.data.filename}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("signing.external.askedBy", { org: view.data.orgName })}
            </p>

            <dl className="border-line mt-6 flex flex-col gap-2 border-t pt-4">
              <div className="flex justify-between gap-4">
                <dt className="text-ink-soft text-[13px]">
                  {t("signing.external.youLabel")}
                </dt>
                <dd className="text-ink truncate text-[13px]">
                  {view.data.signerName || view.data.signerEmail}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-ink-soft text-[13px]">
                  {t("signing.external.progressLabel")}
                </dt>
                <dd className="text-ink text-[13px]">
                  {t("signing.external.progress", {
                    signed: view.data.signedCount,
                    total: view.data.signerCount,
                  })}
                  {" · "}
                  {modeLabel(t, view.data.mode)}
                </dd>
              </div>
            </dl>

            {view.data.message && (
              <p className="text-ink-soft mt-4 text-[13px] whitespace-pre-line">
                {view.data.message}
              </p>
            )}

            <div className="mt-6 flex flex-col gap-3">
              <a
                href={externalDocumentUrl(signToken)}
                target="_blank"
                rel="noreferrer"
                className="text-link text-center text-[13px] underline"
              >
                {t("signing.external.reviewDocument")}
              </a>

              {/* A failed attempt leaves the request open and this signee able to try
                  again, so say what happened rather than silently offering the button. */}
              {view.data.signerStatus === SIGNER_STATUS.failed && (
                <p className="text-error text-[13px]">
                  {t("signing.external.attemptFailed")}
                </p>
              )}

              {!view.data.hasCredential ? (
                <>
                  <p className="text-ink-soft text-[13px]">
                    {t("signing.external.needsCredential")}
                  </p>
                  <Button
                    type="button"
                    onClick={onLink}
                    loading={link.isPending}
                  >
                    {t("signing.external.linkButton")}
                  </Button>
                </>
              ) : view.data.canSign ? (
                <Button type="button" onClick={onSign} loading={sign.isPending}>
                  {t("signing.external.signButton")}
                </Button>
              ) : (
                <p className="text-ink-soft text-[13px]">
                  {t("signing.external.notYourTurn")}
                </p>
              )}

              {/* Refusing up front does not require a linked credential or a
                  completed turn — it is offered whenever this signee is still
                  pending, same as the member's "To sign" list. */}
              <Button
                type="button"
                variant="secondary"
                onClick={() => setDeclining(true)}
              >
                {t("signing.external.declineButton")}
              </Button>
            </div>

            <p className="text-muted mt-4 text-[12px]">
              {t("signing.external.walletHint")}
            </p>
          </>
        )}
      </Card>
      {declining && (
        <DeclineDialog
          busy={decline.isPending}
          onConfirm={onDecline}
          onClose={() => setDeclining(false)}
        />
      )}
    </div>
  );
}
