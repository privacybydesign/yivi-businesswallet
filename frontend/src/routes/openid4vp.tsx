import { useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { startOpenID4VPTransaction } from "../api/openid4vp";
import type { StartErrorKind } from "../lib/openid4vp-invocation";
import { parseInvocation, startErrorKind } from "../lib/openid4vp-invocation";
import { Card, Logo, Outcome } from "../ui";

// The address a verifier redirects the browser to (or encodes in a QR):
// GET /openid4vp?client_id=…&request_uri=…. It is an SPA route — the production
// server's SPA fallback serves it, no backend route exists — and this component
// hands the verifier's parameters to the backend once, then replaces the URL with
// the opaque transaction id so nothing verifier-supplied survives in history or
// rides through the login redirect.
export default function OpenID4VP(): React.JSX.Element {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const invocation = useMemo(
    () => parseInvocation(location.search),
    [location.search],
  );
  const [startError, setStartError] = useState<StartErrorKind>();
  const error =
    invocation === null ? ("invalidInvocation" as const) : startError;

  useEffect(() => {
    if (invocation === null) {
      return;
    }
    const controller = new AbortController();
    startOpenID4VPTransaction(invocation, controller.signal)
      .then((id) => {
        void navigate(`/openid4vp/${encodeURIComponent(id)}`, {
          replace: true,
        });
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) {
          return;
        }
        setStartError(startErrorKind(err));
      });
    return () => controller.abort();
  }, [invocation, navigate]);

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        {error === undefined ? (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("openid4vp.starting")}
          </p>
        ) : (
          <Outcome
            tone="error"
            icon="warning"
            title={t("openid4vp.invalidTitle")}
            message={t(`openid4vp.startErrors.${error}`)}
          />
        )}
      </Card>
    </div>
  );
}
