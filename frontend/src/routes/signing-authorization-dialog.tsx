import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { SigningStart } from "../api/signing";
import { useStartYiviCscAuthorizationQuery } from "../api/yivi-csc.queries";
import { useSigningAuthorization } from "../lib/use-signing-authorization";
import { Modal } from "../ui";
import { SigningAuthorizationPanel } from "./signing-authorization-panel";

// The Yivi step of a signing ceremony, shown on the page that started it. The
// built-in signing service answers this page with an authorization id instead
// of redirecting the browser to its own authorization page, so the signer scans
// the QR code where they pressed Sign and never leaves for a page with nothing
// but a QR code on it. Render it only for a start with authorizeInApp; any other
// provider's authorization page is its own and the browser has to go there.
export function SigningAuthorizationDialog({
  start,
  onClose,
}: Readonly<{
  start: SigningStart;
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

  const authorizationId = started.data?.authorizationId;
  if (authorizationId === undefined) {
    return (
      <Modal
        title={t("signing.authorize.dialogTitle")}
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
  return <Started authorizationId={authorizationId} onClose={onClose} />;
}

function Started({
  authorizationId,
  onClose,
}: Readonly<{
  authorizationId: string;
  onClose: () => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const state = useSigningAuthorization(authorizationId);
  const { view, authorization, cancel } = state;

  // Closing must not strand the ceremony: it holds the document until the
  // service's redirect ends it. So closing a pending authorization cancels it,
  // and a refused one goes on to that redirect; only an authorization that is
  // gone (expired, unreadable) is simply closed.
  const close = (): void => {
    if (view === "pending") {
      cancel.mutate();
    } else if (view === "denied" && authorization?.redirectUrl !== undefined) {
      window.location.assign(authorization.redirectUrl);
    } else if (view !== "leaving") {
      onClose();
    }
  };

  return (
    <Modal
      title={t("signing.authorize.dialogTitle")}
      closeLabel={t("common.close")}
      onClose={close}
      // A stray click outside must not cancel a signature in progress.
      dismissible={false}
    >
      <SigningAuthorizationPanel state={state} embedded />
    </Modal>
  );
}
