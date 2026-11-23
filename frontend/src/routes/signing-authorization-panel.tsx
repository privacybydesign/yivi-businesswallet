import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  AUTHORIZATION_DENIAL,
  AUTHORIZATION_PURPOSE,
  AUTHORIZATION_SOURCE,
  AUTHORIZATION_STATUS,
} from "../api/yivi-csc";
import type { YiviCscAuthorization } from "../api/yivi-csc";
import type { SigningAuthorizationState } from "../lib/use-signing-authorization";
import { yiviUniversalLink } from "../lib/yivi-universal-link";
import { Button, Outcome, buttonClassName } from "../ui";
import { Countdown, QrCode } from "./proofing-verify-steps";

// The Yivi step of a signing ceremony on the built-in signing service: what is
// being approved, the QR code for the Yivi app, and how it ended. It is the
// whole of the standalone authorization page and the body of the dialog the
// wallet's own signing pages open, so the step reads the same in both.

const HINT = "text-ink-soft text-[13px]";

// Which data to disclose for a signature: the one the certificate was issued
// on. None when the service names no source, or one this page has no copy for.
function sourceHint(
  source: string | undefined,
  t: TFunction,
): string | undefined {
  switch (source) {
    case AUTHORIZATION_SOURCE.mrtd:
      return t("signing.authorize.sign.sourceMrtd");
    case AUTHORIZATION_SOURCE.brp:
      return t("signing.authorize.sign.sourceBrp");
    default:
      return undefined;
  }
}

function denialMessage(reason: string | undefined, t: TFunction): string {
  switch (reason) {
    case AUTHORIZATION_DENIAL.identityMismatch:
      return t("signing.authorize.denied.identityMismatch");
    case AUTHORIZATION_DENIAL.identityIncomplete:
      return t("signing.authorize.denied.identityIncomplete");
    default:
      return t("signing.authorize.denied.generic");
  }
}

// What is being approved, in words. In a dialog the dialog's own title is the
// first heading, so this one sits a level below it and closer to the top.
function Purpose({
  authorization,
  embedded,
}: Readonly<{
  authorization: YiviCscAuthorization;
  embedded: boolean;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const Heading = embedded ? "h3" : "h1";
  const headingClass = `${embedded ? "" : "mt-6 "}text-center font-bold break-words ${
    embedded ? "text-[18px]" : "text-[22px]"
  }`;
  if (authorization.purpose === AUTHORIZATION_PURPOSE.link) {
    return (
      <>
        <Heading className={headingClass}>
          {t("signing.authorize.link.title")}
        </Heading>
        <p className={`${HINT} mt-2 text-center`}>
          {t("signing.authorize.link.choose")}
        </p>
        <p className={`${HINT} mt-2 text-center`}>
          {t("signing.authorize.link.name")}
        </p>
      </>
    );
  }
  const source = sourceHint(authorization.source, t);
  return (
    <>
      <Heading className={headingClass}>
        {authorization.description
          ? t("signing.authorize.sign.title", {
              document: authorization.description,
            })
          : t("signing.authorize.sign.titleUnnamed")}
      </Heading>
      <p className={`${HINT} mt-2 text-center`}>
        {t("signing.authorize.sign.intro")}
      </p>
      {source !== undefined && (
        <p className={`${HINT} mt-2 text-center`}>{source}</p>
      )}
    </>
  );
}

export function SigningAuthorizationPanel({
  state,
  onContinue,
}: Readonly<{
  state: SigningAuthorizationState;
  // Given inside a dialog on one of the wallet's own pages, which ends a refused
  // authorization itself. Without it the signer follows the redirect as a link.
  onContinue?: (redirectUrl: string) => void;
}>): React.JSX.Element {
  const embedded = onContinue !== undefined;
  const { t } = useTranslation();
  const { authorization, view, secondsLeft, cancel } = state;
  const walletUrl =
    authorization?.walletLink === undefined
      ? undefined
      : yiviUniversalLink(authorization.walletLink);
  const note = `text-ink-soft ${embedded ? "" : "mt-6 "}block text-center text-[14px]`;

  return (
    <>
      {view === "loading" && <p className={note}>{t("common.loading")}</p>}
      {view === "leaving" && (
        <output className={note}>
          {!embedded
            ? t("signing.authorize.leaving")
            : authorization?.status === AUTHORIZATION_STATUS.approved
              ? t("signing.authorize.finishing")
              : t("signing.authorize.cancelling")}
        </output>
      )}
      {view === "expired" && (
        <Outcome
          tone="info"
          icon="time"
          title={t("signing.authorize.expired.title")}
          message={t("signing.authorize.expired.message")}
        />
      )}
      {view === "error" && (
        <Outcome
          tone="error"
          icon="warning"
          title={t("signing.authorize.error.title")}
          message={t("signing.authorize.error.message")}
        />
      )}
      {view === "denied" && authorization !== undefined && (
        <Outcome
          tone="error"
          icon="invalid"
          role="alert"
          title={t("signing.authorize.denied.title")}
          message={denialMessage(authorization.reason, t)}
          action={
            authorization.redirectUrl !== undefined &&
            (onContinue === undefined ? (
              <a
                href={authorization.redirectUrl}
                className={buttonClassName("primary")}
              >
                {t("signing.authorize.denied.continue")}
              </a>
            ) : (
              <Button
                type="button"
                onClick={() => onContinue(authorization.redirectUrl ?? "")}
              >
                {t("common.close")}
              </Button>
            ))
          }
        />
      )}
      {view === "pending" && authorization !== undefined && (
        <>
          <Purpose authorization={authorization} embedded={embedded} />
          <div className="mt-6 flex flex-col items-center gap-4">
            {walletUrl !== undefined && (
              <>
                <QrCode value={walletUrl} />
                <p className={`${HINT} text-center`}>
                  {t("signing.authorize.scanHint")}
                </p>
                <a href={walletUrl} className={buttonClassName("primary")}>
                  {t("signing.authorize.openYivi")}
                </a>
              </>
            )}
            <Countdown seconds={secondsLeft} />
            {cancel.isError && (
              <p role="alert" className="text-error text-[13px]">
                {t("signing.authorize.cancelError")}
              </p>
            )}
            <Button
              type="button"
              variant="secondary"
              loading={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              {t("common.cancel")}
            </Button>
          </div>
        </>
      )}
    </>
  );
}
