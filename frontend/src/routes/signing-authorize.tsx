import { useEffect } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import {
  AUTHORIZATION_DENIAL,
  AUTHORIZATION_PURPOSE,
  AUTHORIZATION_SOURCE,
} from "../api/yivi-csc";
import type { YiviCscAuthorization } from "../api/yivi-csc";
import {
  useCancelYiviCscAuthorizationMutation,
  useYiviCscAuthorizationQuery,
} from "../api/yivi-csc.queries";
import {
  authorizationAutoRedirect,
  authorizationView,
} from "../lib/signing-authorization";
import { useSecondsLeft } from "../lib/use-seconds-left";
import { yiviUniversalLink } from "../lib/yivi-universal-link";
import { Button, Card, Logo, Outcome, buttonClassName } from "../ui";
import { Countdown, QrCode } from "./proofing-verify-steps";

// The built-in signing service's authorization page. A signing ceremony sends
// the signer's browser here to approve one thing in the Yivi app: identifying
// themselves for their signing certificate, or one signature. The signer has no
// session here, a member or not: the authorization id in the address is the
// only key. Once decided, the service names where the browser goes next, a full
// URL on the API that carries the ceremony on.

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

// What is being approved, in words.
function Purpose({
  authorization,
}: Readonly<{ authorization: YiviCscAuthorization }>): React.JSX.Element {
  const { t } = useTranslation();
  if (authorization.purpose === AUTHORIZATION_PURPOSE.link) {
    return (
      <>
        <h1 className="mt-6 text-center text-[22px] font-bold">
          {t("signing.authorize.link.title")}
        </h1>
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
      <h1 className="mt-6 text-center text-[22px] font-bold break-words">
        {authorization.description
          ? t("signing.authorize.sign.title", {
              document: authorization.description,
            })
          : t("signing.authorize.sign.titleUnnamed")}
      </h1>
      <p className={`${HINT} mt-2 text-center`}>
        {t("signing.authorize.sign.intro")}
      </p>
      {source !== undefined && (
        <p className={`${HINT} mt-2 text-center`}>{source}</p>
      )}
    </>
  );
}

export default function SigningAuthorize(): React.JSX.Element {
  const { t } = useTranslation();
  const { id } = useParams();
  // Guaranteed by the ":id" route segment this component mounts under.
  const authorizationId = id!;

  const query = useYiviCscAuthorizationQuery(authorizationId);
  const cancel = useCancelYiviCscAuthorizationMutation(authorizationId);
  const authorization = query.data;
  const secondsLeft = useSecondsLeft(authorization?.expiresAt);
  const view = authorizationView({
    authorization,
    error: query.error,
    secondsLeft,
  });

  // Approved, or cancelled by the signer: on to where the service says, with
  // nothing to read first.
  const redirectUrl = authorizationAutoRedirect(authorization);
  useEffect(() => {
    if (redirectUrl !== undefined) window.location.assign(redirectUrl);
  }, [redirectUrl]);

  const walletUrl =
    authorization?.walletLink === undefined
      ? undefined
      : yiviUniversalLink(authorization.walletLink);

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-6 sm:p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {view === "loading" && (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("common.loading")}
          </p>
        )}
        {view === "leaving" && (
          <output className="text-ink-soft mt-6 block text-center text-[14px]">
            {t("signing.authorize.leaving")}
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
              authorization.redirectUrl !== undefined && (
                <a
                  href={authorization.redirectUrl}
                  className={buttonClassName("primary")}
                >
                  {t("signing.authorize.denied.continue")}
                </a>
              )
            }
          />
        )}
        {view === "pending" && authorization !== undefined && (
          <>
            <Purpose authorization={authorization} />
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
      </Card>
    </div>
  );
}
