import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import * as yivi from "@privacybydesign/yivi-frontend";
import "@privacybydesign/yivi-css";
import { ApiError } from "../api/http";
import { claimAuthSession } from "../api/auth";
import type { PendingInvitation } from "../api/auth";
import { meQueryKey } from "../api/auth.queries";
import {
  acceptInvitationById,
  INVITATION_SESSION_URL,
} from "../api/invitations";
import { inviteError } from "../lib/invite-error";
import type { InviteErrorContent } from "../lib/invite-error";
import {
  Avatar,
  Button,
  Card,
  Icon,
  IdentityDisclosure,
  Logo,
  Outcome,
} from "../ui";
import * as React from "react";

const YIVI_ELEMENT_ID = "yivi-web-form";
const AUTH_SESSION_URL = "/api/v1/auth/session";

type LoginPhase =
  | "running"
  | "claiming"
  | "invited"
  | "disclosing"
  | "accepting"
  | "accepted"
  | "pendingReview"
  | "acceptError"
  | "idle";

export default function Login(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<LoginPhase>("running");
  const [message, setMessage] = useState<string>("");
  const [invites, setInvites] = useState<PendingInvitation[]>([]);
  const [chosen, setChosen] = useState<PendingInvitation | null>(null);
  const [errorContent, setErrorContent] = useState<InviteErrorContent>(() =>
    inviteError(null, t),
  );

  useEffect(() => {
    let cancelled = false;
    let sessionToken = "";

    const yiviWeb = yivi.newWeb({
      debugging: import.meta.env.DEV,
      element: `#${YIVI_ELEMENT_ID}`,
      minimal: true,
      session: {
        url: "",
        start: { url: () => AUTH_SESSION_URL, method: "POST" },
        mapping: {
          sessionToken: (r) => (sessionToken = (r as { token: string }).token),
        },
        result: false,
      },
    });

    yiviWeb
      .start()
      .then(async () => {
        if (cancelled) return;
        setPhase("claiming");
        try {
          const result = await claimAuthSession(sessionToken);
          if (cancelled) return;
          if ("pendingInvitations" in result) {
            setInvites(result.pendingInvitations);
            setPhase("invited");
            return;
          }
          queryClient.setQueryData(meQueryKey, result);
          void navigate("/");
        } catch (error) {
          if (cancelled) return;
          handleClaimError(error, setPhase, setMessage, t);
        }
      })
      .catch(() => {
        if (cancelled) return;
        setPhase("idle");
        setMessage(t("login.notCompleted"));
      });

    return () => {
      cancelled = true;
      void yiviWeb.abort();
    };
  }, [navigate, queryClient, t]);

  const onAcceptToken = (disclosureToken: string): void => {
    if (chosen == null) return;
    setPhase("accepting");
    acceptInvitationById(chosen.id, disclosureToken)
      .then((result) => {
        setPhase(result.status === "accepted" ? "accepted" : "pendingReview");
      })
      .catch((error: unknown) => {
        setErrorContent(inviteError(error, t));
        setPhase("acceptError");
      });
  };

  // Accept already minted a session; refresh `me` (cached as null) before
  // entering so the protected routes see the authenticated user.
  const enterApp = async (): Promise<void> => {
    await queryClient.invalidateQueries({ queryKey: meQueryKey });
    void navigate("/");
  };

  const showMessage = phase === "idle" && message !== "";

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {phase === "accepted" ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("inviteAccept.accepted", {
              org: chosen?.organizationName ?? "",
            })}
            message={t("inviteAccept.acceptedHint")}
            action={
              <Button variant="primary" onClick={() => void enterApp()}>
                {t("inviteAccept.goToApp")}
              </Button>
            }
          />
        ) : phase === "pendingReview" ? (
          <Outcome
            tone="info"
            icon="time"
            title={t("inviteAccept.pendingReview")}
            message={t("inviteAccept.pendingReviewHint")}
          />
        ) : phase === "acceptError" ? (
          <Outcome
            tone="error"
            icon="warning"
            title={errorContent.title}
            message={errorContent.body}
            action={
              <Button variant="secondary" onClick={() => setPhase("invited")}>
                {t("inviteAccept.retry")}
              </Button>
            }
          />
        ) : phase === "disclosing" ? (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {t("inviteAccept.title")}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("inviteAccept.scanPrompt", {
                org: chosen?.organizationName ?? "",
              })}
            </p>
            <div className="mt-6 flex justify-center">
              <IdentityDisclosure
                sessionUrl={INVITATION_SESSION_URL}
                onToken={onAcceptToken}
                onAborted={() => setPhase("invited")}
              />
            </div>
          </>
        ) : phase === "invited" ? (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {t("inviteAccept.title")}
            </h1>
            <p className="text-ink-soft mt-2 text-center text-[14px]">
              {t("inviteAccept.invitedIntro")}
            </p>
            <div className="mt-5 flex flex-col gap-2">
              {invites.map((invite) => (
                <button
                  key={invite.id}
                  type="button"
                  onClick={() => {
                    setChosen(invite);
                    setPhase("disclosing");
                  }}
                  className="border-line-strong hover:bg-surface-3 rounded-yivi bg-surface flex w-full cursor-pointer items-center gap-3 border px-3 py-2.5 text-left transition-colors"
                >
                  <Avatar name={invite.organizationName} tone="rose" />
                  <div className="min-w-0 flex-1">
                    <div className="text-ink truncate text-[14px] font-semibold">
                      {invite.organizationName}
                    </div>
                    <div className="text-muted truncate font-mono text-[12px]">
                      {invite.organizationSlug}
                    </div>
                  </div>
                  <Icon
                    name="chevron_right"
                    size={16}
                    className="text-muted shrink-0"
                  />
                </button>
              ))}
            </div>
          </>
        ) : (
          <>
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

            <div
              className={showMessage ? "hidden" : "mt-6 flex justify-center"}
            >
              <div id={YIVI_ELEMENT_ID} />
            </div>
          </>
        )}
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
  setPhase("idle");
  if (error instanceof ApiError && error.status === 422) {
    setMessage(t("login.credentialRejected"));
    return;
  }
  setMessage(t("login.failed"));
}
