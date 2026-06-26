import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import * as yivi from "@privacybydesign/yivi-frontend";
import "@privacybydesign/yivi-css";
import { ApiError } from "../api/http";
import { claimAuthSession } from "../api/auth";
import { meQueryKey } from "../api/auth.queries";
import { Card, Logo } from "../ui";
import * as React from "react";

const YIVI_ELEMENT_ID = "yivi-web-form";
const AUTH_SESSION_URL = "/api/v1/auth/session";

type LoginPhase = "idle" | "running" | "claiming" | "error";

export default function Login(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<LoginPhase>("running");
  const [message, setMessage] = useState<string>("");

  useEffect(() => {
    let cancelled = false;
    let sessionToken = "";

    const yiviWeb = yivi.newWeb({
      debugging: import.meta.env.DEV,
      element: `#${YIVI_ELEMENT_ID}`,
      minimal: true,
      session: {
        url: "",
        start: {
          url: () => AUTH_SESSION_URL,
          method: "POST",
        },
        mapping: {
          sessionToken: (r) => (sessionToken = (r as { token: string }).token),
        },
        result: false,
      },
    });

    yiviWeb
      .start()
      .then(async () => {
        if (cancelled) {
          return;
        }
        setPhase("claiming");
        try {
          const me = await claimAuthSession(sessionToken);
          queryClient.setQueryData(meQueryKey, me);
          if (!cancelled) {
            void navigate("/");
          }
        } catch (error) {
          if (cancelled) {
            return;
          }
          handleClaimError(error, setPhase, setMessage, t);
        }
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setPhase("idle");
        setMessage(t("login.notCompleted"));
      });

    return () => {
      cancelled = true;
      void yiviWeb.abort();
    };
  }, [navigate, queryClient, t]);

  const showMessage = phase === "error" || (phase === "idle" && message !== "");

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        <h1 className="mt-6 text-center text-[24px] font-bold">
          {t("login.title")}
        </h1>
        <p className="text-ink-soft mt-1 text-center text-[14px]">
          {t("login.subtitle")}
        </p>

        {phase === "claiming" && (
          <p className="text-ink-soft mt-4 text-center text-[14px]">
            {t("login.completing")}
          </p>
        )}
        {showMessage && (
          <p
            role="alert"
            className="rounded-yivi bg-error-bg text-error mt-4 px-3 py-2 text-center text-[13px]"
          >
            {message}
          </p>
        )}

        <div className="mt-6 flex justify-center">
          <div id={YIVI_ELEMENT_ID} />
        </div>
      </Card>
    </div>
  );
}

function handleClaimError(
  error: unknown,
  setPhase: (p: LoginPhase) => void,
  setMessage: (m: string) => void,
  t: TFunction,
): void {
  if (error instanceof ApiError && error.status === 422) {
    setPhase("error");
    setMessage(t("login.credentialRejected"));
    return;
  }
  setPhase("idle");
  setMessage(t("login.failed"));
}
