import { useCallback, useEffect } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { finishSigningCeremony } from "../api/signing";
import type { SigningStart } from "../api/signing";
import { useStartYiviCscAuthorizationQuery } from "../api/yivi-csc.queries";
import type { CeremonyEnd } from "../lib/signing-authorization-start";
import { useSigningAuthorization } from "../lib/use-signing-authorization";
import { Modal } from "../ui";
import { SigningAuthorizationPanel } from "./signing-authorization-panel";

// The Yivi step of a signing ceremony, shown on the page that started it. The
// built-in signing service answers this page with an authorization id instead
// of redirecting the browser to its own authorization page, and the ceremony's
// callback answers it with how the ceremony ended instead of redirecting back.
// So the signer scans the QR code where they pressed Sign and the page never
// reloads. Render it only for a start with authorizeInApp; any other provider's
// authorization page is its own and the browser has to go there.
export function SigningAuthorizationDialog({
  start,
  step,
  onFinished,
  onClose,
}: Readonly<{
  start: SigningStart;
  // Where this step is in a ceremony of more than one, to say so.
  step?: { position: number; total: number };
  // The ceremony ended, whichever way: approved, refused or cancelled.
  onFinished: (end: CeremonyEnd) => void;
  // The dialog was closed with nothing left to end.
  onClose: () => void;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  const started = useStartYiviCscAuthorizationQuery(start.authorizeUrl);

  // Where the browser has to go after all: to the service's own page when this
  // one could not start the authorization, or on to the ceremony's callback
  // when the service refused to start one. Either shows what went wrong.
  const fallbackUrl = started.isError
    ? start.authorizeUrl
    : started.data !== undefined && started.data.authorizationId === undefined
      ? started.data.redirectUrl
      : undefined;
  useEffect(() => {
    if (fallbackUrl !== undefined) window.location.assign(fallbackUrl);
  }, [fallbackUrl]);

  const title =
    step !== undefined && step.total > 1
      ? t("signing.authorize.dialogTitleStep", {
          position: step.position,
          total: step.total,
        })
      : t("signing.authorize.dialogTitle");

  const authorizationId = started.data?.authorizationId;
  if (authorizationId === undefined) {
    return (
      <Modal
        title={title}
        closeLabel={t("common.close")}
        onClose={onClose}
        dismissible={false}
      >
        <p className="text-ink-soft text-center text-[14px]">
          {t("common.loading")}
        </p>
      </Modal>
    );
  }
  return (
    <Started
      // A ceremony's next step is a new authorization: nothing of the last one
      // (its countdown, a followed redirect) may carry over.
      key={authorizationId}
      authorizationId={authorizationId}
      title={title}
      onFinished={onFinished}
      onClose={onClose}
    />
  );
}

function Started({
  authorizationId,
  title,
  onFinished,
  onClose,
}: Readonly<{
  authorizationId: string;
  title: string;
  onFinished: (end: CeremonyEnd) => void;
  onClose: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();

  // The decided authorization's redirect is the ceremony's callback. Asked for
  // JSON it does its work and says how the ceremony ended. If that fails, the
  // browser follows the redirect as it always did.
  const finish = useCallback(
    (redirectUrl: string): void => {
      finishSigningCeremony(redirectUrl)
        .then(onFinished)
        .catch(() => window.location.assign(redirectUrl));
    },
    [onFinished],
  );
  const state = useSigningAuthorization(authorizationId, finish);
  const { view, authorization, cancel } = state;

  // Closing must not strand the ceremony: it holds the document until the
  // service's redirect ends it. So closing a pending authorization cancels it,
  // and a refused one is ended through its redirect; only an authorization that
  // is gone (expired, unreadable) is simply closed.
  const close = (): void => {
    if (view === "pending") {
      cancel.mutate();
    } else if (view === "denied" && authorization?.redirectUrl !== undefined) {
      finish(authorization.redirectUrl);
    } else if (view !== "leaving") {
      onClose();
    }
  };

  return (
    <Modal
      title={title}
      closeLabel={t("common.close")}
      onClose={close}
      // A stray click outside must not cancel a signature in progress.
      dismissible={false}
    >
      <SigningAuthorizationPanel state={state} onContinue={finish} />
    </Modal>
  );
}
