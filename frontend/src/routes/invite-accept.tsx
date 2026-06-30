import * as React from "react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { ApiError } from "../api/http";
import {
  acceptInviteByToken,
  declineInviteByToken,
  inviteSessionUrl,
} from "../api/invitations";
import { useInvitePreviewQuery } from "../api/invitations.queries";
import { Avatar, Button, Card, IdentityDisclosure, Logo, Outcome } from "../ui";

type Phase =
  | "preview"
  | "disclosing"
  | "accepting"
  | "declining"
  | "accepted"
  | "pendingReview"
  | "declined"
  | "error";

type ErrorKey =
  | "inviteAccept.failed"
  | "inviteAccept.nameMismatch"
  | "inviteAccept.emailMismatch"
  | "inviteAccept.alreadyMember"
  | "inviteAccept.disclosureFailed"
  | "inviteAccept.expired"
  | "inviteAccept.notFound";

function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    typeof error.body === "object" &&
    error.body !== null &&
    "code" in error.body &&
    typeof error.body.code === "string"
  ) {
    return error.body.code;
  }
  return null;
}

export default function InviteAccept(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { token } = useParams();
  const inviteToken = token ?? "";
  const preview = useInvitePreviewQuery(inviteToken);
  const [phase, setPhase] = useState<Phase>("preview");
  const [errorKey, setErrorKey] = useState<ErrorKey>("inviteAccept.failed");

  const dateFormatter = React.useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }),
    [i18n.language],
  );

  const orgName = preview.data?.organizationName ?? "";

  const fail = (key: ErrorKey): void => {
    setErrorKey(key);
    setPhase("error");
  };

  const onToken = (disclosureToken: string): void => {
    setPhase("accepting");
    acceptInviteByToken(inviteToken, disclosureToken)
      .then((result) => {
        setPhase(result.status === "accepted" ? "accepted" : "pendingReview");
      })
      .catch((error: unknown) => {
        switch (errorCode(error)) {
          case "name_mismatch":
            return fail("inviteAccept.nameMismatch");
          case "email_mismatch":
            return fail("inviteAccept.emailMismatch");
          case "already_member":
            return fail("inviteAccept.alreadyMember");
          case "disclosure_failed":
            return fail("inviteAccept.disclosureFailed");
          case "invitation_expired":
            return fail("inviteAccept.expired");
          case "invitation_not_found":
            return fail("inviteAccept.notFound");
          default:
            return fail("inviteAccept.failed");
        }
      });
  };

  const onDecline = (): void => {
    setPhase("declining");
    declineInviteByToken(inviteToken)
      .then(() => setPhase("declined"))
      .catch(() => fail("inviteAccept.failed"));
  };

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {preview.isPending ? (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("inviteAccept.loading")}
          </p>
        ) : preview.isError ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("inviteAccept.title")}
            message={t(
              errorCode(preview.error) === "invitation_expired"
                ? "inviteAccept.expired"
                : "inviteAccept.notFound",
            )}
          />
        ) : phase === "accepted" ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("inviteAccept.accepted", { org: orgName })}
            message={t("inviteAccept.acceptedHint")}
            action={
              <Link to="/login">
                <Button variant="primary">{t("inviteAccept.goToApp")}</Button>
              </Link>
            }
          />
        ) : phase === "pendingReview" ? (
          <Outcome
            tone="info"
            icon="time"
            title={t("inviteAccept.pendingReview")}
            message={t("inviteAccept.pendingReviewHint")}
          />
        ) : phase === "declined" ? (
          <Outcome
            tone="info"
            icon="valid"
            title={t("inviteAccept.declined")}
            message={t("inviteAccept.declinedHint")}
          />
        ) : phase === "error" ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("inviteAccept.title")}
            message={t(errorKey)}
            action={
              <Button variant="secondary" onClick={() => setPhase("preview")}>
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
              {t("inviteAccept.scanPrompt", { org: orgName })}
            </p>
            <div className="mt-6 flex justify-center">
              <IdentityDisclosure
                sessionUrl={inviteSessionUrl(inviteToken)}
                onToken={onToken}
                onAborted={() => setPhase("preview")}
              />
            </div>
          </>
        ) : (
          <>
            <div className="mt-6 flex flex-col items-center text-center">
              <Avatar name={orgName} tone="rose" size="lg" />
              <h1 className="text-ink mt-4 text-[22px] font-bold">
                {t("inviteAccept.title")}
              </h1>
              <p className="text-ink-soft mt-1 text-[14px]">
                {t("inviteAccept.invitedTo", { org: orgName })}
              </p>
            </div>

            <div className="rounded-yivi bg-surface-2 mt-6 px-4 py-3 text-center text-[13.5px]">
              <div className="text-ink font-semibold">
                {t("inviteAccept.invitedAs", {
                  name: `${preview.data.givenNames} ${preview.data.lastName}`,
                })}
              </div>
              <div className="text-ink-soft mt-0.5">
                {t("inviteAccept.forEmail", { email: preview.data.email })}
              </div>
              <div className="text-ink-soft mt-0.5">
                {t("inviteAccept.expires", {
                  date: dateFormatter.format(new Date(preview.data.expiresAt)),
                })}
              </div>
            </div>

            <Button
              variant="primary"
              className="mt-6 w-full"
              onClick={() => setPhase("disclosing")}
              loading={phase === "accepting"}
              disabled={phase === "declining"}
            >
              {t("inviteAccept.join", { org: orgName })}
            </Button>
            <button
              type="button"
              onClick={onDecline}
              disabled={phase === "declining" || phase === "accepting"}
              className="text-muted hover:text-ink mx-auto mt-4 block cursor-pointer text-[13px] underline-offset-2 transition-colors hover:underline disabled:cursor-not-allowed disabled:opacity-60"
            >
              {phase === "declining"
                ? t("inviteAccept.declining")
                : t("inviteAccept.decline")}
            </button>
          </>
        )}
      </Card>
    </div>
  );
}
