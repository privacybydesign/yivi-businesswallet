import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import * as yivi from "@privacybydesign/yivi-frontend";
import "@privacybydesign/yivi-css";
import { ApiError } from "../api/http";
import { claimAuthSession } from "../api/auth";
import { meQueryKey } from "../api/auth.queries";

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

  return (
    <section>
      <h1>Login</h1>
      {phase === "claiming" && <p>Completing sign-in…</p>}
      {phase === "error" && <p role="alert">{message}</p>}
      {phase === "idle" && message !== "" && <p role="alert">{message}</p>}
      <div id={YIVI_ELEMENT_ID} />
    </section>
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
