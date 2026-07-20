import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { claimAuthSession } from "../api/auth";
import type { PendingInvitation } from "../api/auth";
import { meQueryKey } from "../api/auth.queries";
import {
  acceptInvitationById,
  INVITATION_SESSION_URL,
} from "../api/invitations";
import { inviteError } from "../lib/invite-error";
import type { InviteErrorContent } from "../lib/invite-error";
import { claimErrorKind } from "../lib/login-error";
import {
  Avatar,
  Button,
  Card,
  Icon,
  IdentityDisclosure,
  LanguageSwitcher,
  Logo,
  Outcome,
} from "../ui";
import type { IconName } from "../ui";
import * as React from "react";

const AUTH_SESSION_URL = "/api/v1/auth/session";

// Marketing highlights shown in the landing hero beside the sign-in card. `title`
// and `body` are i18n keys resolved at render (labels aren't hard-coded here).
const FEATURES = [
  {
    key: "passwordless",
    icon: "lock",
    title: "landing.features.passwordless.title",
    body: "landing.features.passwordless.body",
  },
  {
    key: "verified",
    icon: "valid",
    title: "landing.features.verified.title",
    body: "landing.features.verified.body",
  },
  {
    key: "delivery",
    icon: "email",
    title: "landing.features.delivery.title",
    body: "landing.features.delivery.body",
  },
  {
    key: "attestations",
    icon: "personal",
    title: "landing.features.attestations.title",
    body: "landing.features.attestations.body",
  },
] as const satisfies ReadonlyArray<{
  key: string;
  icon: IconName;
  title: string;
  body: string;
}>;

type LoginPhase =
  | "running"
  | "claiming"
  | "invited"
  | "notRegistered"
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

  const handleLoginToken = (id: string): void => {
    setPhase("claiming");
    claimAuthSession(id)
      .then((result) => {
        if ("pendingInvitations" in result) {
          setInvites(result.pendingInvitations);
          setPhase("invited");
          return;
        }
        queryClient.setQueryData(meQueryKey, result);
        void navigate("/");
      })
      .catch((error: unknown) => {
        handleClaimError(error, setPhase, setMessage, t);
      });
  };

  const handleAbort = (): void => {
    setPhase("idle");
    setMessage(t("login.notCompleted"));
  };

  const retry = (): void => {
    setMessage("");
    setPhase("running");
  };

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

  // Accept already minted a session; force-refetch `me` (cached as null from the
  // pre-login 401, with no active observer here) before entering, so the
  // protected routes see the authenticated user instead of the stale null.
  const enterApp = async (): Promise<void> => {
    await queryClient.refetchQueries({ queryKey: meQueryKey });
    void navigate("/");
  };

  const showMessage = phase === "idle" && message !== "";

  return (
    <div className="mesh-wave relative flex min-h-screen flex-col justify-center">
      <div className="absolute top-4 right-4 sm:top-6 sm:right-6">
        <LanguageSwitcher />
      </div>
      <div className="mx-auto grid w-full max-w-6xl items-center gap-10 px-6 py-12 lg:grid-cols-2 lg:gap-16">
        <section className="order-2 flex flex-col lg:order-1">
          <h1 className="font-display text-ink text-[32px] leading-[1.15] font-bold sm:text-[38px]">
            {t("landing.headline")}
          </h1>
          <p className="text-ink-soft mt-4 max-w-md text-[15px] leading-relaxed">
            {t("landing.subhead")}
          </p>
          <ul className="mt-8 flex flex-col gap-5">
            {FEATURES.map((feature) => (
              <li key={feature.key} className="flex items-start gap-3.5">
                <span className="bg-highlight text-link rounded-yivi flex h-9 w-9 shrink-0 items-center justify-center">
                  <Icon name={feature.icon} size={18} />
                </span>
                <div>
                  <div className="text-ink text-[14px] font-semibold">
                    {t(feature.title)}
                  </div>
                  <div className="text-ink-soft text-[13px] leading-snug">
                    {t(feature.body)}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        </section>

        <div className="order-1 flex justify-center lg:order-2 lg:justify-end">
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
                  <Button
                    variant="secondary"
                    onClick={() => setPhase("invited")}
                  >
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
            ) : phase === "notRegistered" ? (
              <Outcome
                tone="info"
                icon="personal"
                title={t("login.notRegistered")}
                message={t("login.notRegisteredHint")}
                action={
                  <div className="flex flex-col gap-2">
                    <Button
                      variant="primary"
                      onClick={() => void navigate("/register")}
                    >
                      {t("login.notRegisteredAction")}
                    </Button>
                    <Button variant="secondary" onClick={retry}>
                      {t("inviteAccept.retry")}
                    </Button>
                  </div>
                }
              />
            ) : (
              <>
                <h1 className="mt-6 text-center text-[24px] font-bold">
                  {t("login.title")}
                </h1>
                <p className="text-ink-soft mt-1 text-center text-[14px]">
                  {t("login.subtitle")}
                </p>

                {phase === "claiming" ? (
                  <p className="text-ink-soft mt-4 text-center text-[14px]">
                    {t("login.completing")}
                  </p>
                ) : showMessage ? (
                  <>
                    <p
                      role="alert"
                      className="rounded-yivi bg-error-bg text-error mt-4 px-3 py-2 text-center text-[13px]"
                    >
                      {message}
                    </p>
                    <div className="mt-4 flex justify-center">
                      <Button variant="secondary" onClick={retry}>
                        {t("inviteAccept.retry")}
                      </Button>
                    </div>
                  </>
                ) : (
                  <>
                    <div className="mt-6 flex justify-center">
                      <IdentityDisclosure
                        sessionUrl={AUTH_SESSION_URL}
                        onToken={handleLoginToken}
                        onAborted={handleAbort}
                      />
                    </div>
                    <p className="text-muted mt-4 flex items-center justify-center gap-1.5 text-center text-[12.5px]">
                      <Icon name="lock" size={13} />
                      {t("login.sharesEmail")}
                    </p>
                  </>
                )}

                <p className="text-ink-soft mt-6 text-center text-[13px]">
                  <button
                    type="button"
                    onClick={() => void navigate("/register")}
                    className="text-primary font-medium hover:underline"
                  >
                    {t("login.registerLink")}
                  </button>
                </p>
              </>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}

function handleClaimError(
  error: unknown,
  setPhase: (p: LoginPhase) => void,
  setMessage: (m: string) => void,
  t: TFunction,
): void {
  switch (claimErrorKind(error)) {
    case "notRegistered":
      setPhase("notRegistered");
      return;
    case "credentialRejected":
      setPhase("idle");
      setMessage(t("login.credentialRejected"));
      return;
    case "failed":
      setPhase("idle");
      setMessage(t("login.failed"));
      return;
  }
}
