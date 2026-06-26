import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
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
          handleClaimError(error, setPhase, setMessage);
        }
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setPhase("idle");
        setMessage("Login was not completed. Please try again.");
      });

    return () => {
      cancelled = true;
      void yiviWeb.abort();
    };
  }, [navigate, queryClient]);

  const showMessage = phase === "error" || (phase === "idle" && message !== "");

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface-2 p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        <h1 className="mt-6 text-center text-[24px] font-bold">Sign in</h1>
        <p className="mt-1 text-center text-[14px] text-ink-soft">
          Use your Yivi app to sign in to the business wallet.
        </p>

        {phase === "claiming" && (
          <p className="mt-4 text-center text-[14px] text-ink-soft">
            Completing sign-in…
          </p>
        )}
        {showMessage && (
          <p
            role="alert"
            className="mt-4 rounded-yivi bg-error-bg px-3 py-2 text-center text-[13px] text-error"
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
): void {
  if (error instanceof ApiError && error.status === 422) {
    setPhase("error");
    setMessage("This credential can't be used to sign in.");
    return;
  }
  setPhase("idle");
  setMessage("Sign-in could not be completed. Please try again.");
}
