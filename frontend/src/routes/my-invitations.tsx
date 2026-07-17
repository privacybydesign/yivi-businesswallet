import * as React from "react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { inviteError } from "../lib/invite-error";
import type { InviteErrorContent } from "../lib/invite-error";
import { INVITATION_SESSION_URL } from "../api/invitations";
import type { MyInvitation } from "../api/invitations";
import {
  useAcceptInvitationByIdMutation,
  useDeclineMyInvitationMutation,
  useMyInvitationsQuery,
} from "../api/invitations.queries";
import {
  myOrganizationsQueryKey,
  organizationsQueryKey,
} from "../api/organization.queries";
import {
  Avatar,
  Button,
  Card,
  IdentityDisclosure,
  Outcome,
  TopBar,
} from "../ui";

type Phase = "list" | "disclosing" | "accepting" | "pendingReview" | "error";

export default function MyInvitations(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const invitations = useMyInvitationsQuery();
  const accept = useAcceptInvitationByIdMutation();
  const decline = useDeclineMyInvitationMutation();

  const [phase, setPhase] = useState<Phase>("list");
  const [active, setActive] = useState<MyInvitation | null>(null);
  const [errorContent, setErrorContent] = useState<InviteErrorContent>(() =>
    inviteError(null, t),
  );

  const dateFormatter = React.useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }),
    [i18n.language],
  );

  const onToken = (disclosureToken: string): void => {
    if (active == null) return;
    setPhase("accepting");
    accept.mutate(
      { id: active.id, disclosureToken },
      {
        onSuccess: (result) => {
          if (result.status === "accepted") {
            void queryClient.invalidateQueries({
              queryKey: myOrganizationsQueryKey,
            });
            void queryClient.invalidateQueries({
              queryKey: organizationsQueryKey,
            });
            void navigate(`/${result.organizationSlug}`);
            return;
          }
          setPhase("pendingReview");
        },
        onError: (error: unknown) => {
          setErrorContent(inviteError(error, t));
          setPhase("error");
        },
      },
    );
  };

  if (phase === "pendingReview") {
    return (
      <InvitationsShell>
        <Outcome
          tone="info"
          icon="time"
          title={t("inviteAccept.pendingReview")}
          message={t("inviteAccept.pendingReviewHint")}
          action={
            <Button variant="secondary" onClick={() => setPhase("list")}>
              {t("myInvitations.back")}
            </Button>
          }
        />
      </InvitationsShell>
    );
  }

  if (phase === "error") {
    return (
      <InvitationsShell>
        <Outcome
          tone="error"
          icon="warning"
          title={errorContent.title}
          message={errorContent.body}
          action={
            <Button variant="secondary" onClick={() => setPhase("list")}>
              {t("inviteAccept.retry")}
            </Button>
          }
        />
      </InvitationsShell>
    );
  }

  if (phase === "disclosing" && active != null) {
    return (
      <InvitationsShell>
        <div className="flex flex-col items-center text-center">
          <p className="text-ink-soft text-[14px]">
            {t("inviteAccept.scanPrompt", { org: active.organizationName })}
          </p>
          <div className="mt-6">
            <IdentityDisclosure
              sessionUrl={INVITATION_SESSION_URL}
              onToken={onToken}
              onAborted={() => setPhase("list")}
            />
          </div>
        </div>
      </InvitationsShell>
    );
  }

  return (
    <>
      <TopBar
        title={t("myInvitations.title")}
        subtitle={t("myInvitations.subtitle")}
      />
      <div className="p-8">
        {invitations.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : invitations.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("myInvitations.loadError", {
                message: invitations.error.message,
              })}
            </p>
          </Card>
        ) : invitations.data.length === 0 ? (
          <Card className="p-8 text-center">
            <p className="text-ink-soft text-[14px]">
              {t("myInvitations.empty")}
            </p>
          </Card>
        ) : (
          <div className="flex flex-col gap-3">
            {invitations.data.map((invite) => (
              <Card key={invite.id} className="flex items-center gap-4 p-4">
                <Avatar name={invite.organizationName} tone="rose" size="lg" />
                <div className="min-w-0 flex-1">
                  <div className="text-ink truncate text-[15px] font-semibold">
                    {invite.organizationName}
                  </div>
                  <div className="text-ink-soft truncate text-[12.5px]">
                    {t("inviteAccept.invitedAs", {
                      name: `${invite.givenNames} ${invite.lastName}`,
                    })}
                  </div>
                  <div className="text-muted truncate text-[12px]">
                    {t("inviteAccept.expires", {
                      date: dateFormatter.format(new Date(invite.expiresAt)),
                    })}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <Button
                    variant="secondary"
                    onClick={() => decline.mutate(invite.id)}
                    disabled={decline.isPending}
                  >
                    {t("myInvitations.decline")}
                  </Button>
                  {invite.reviewStatus === "pending" ? (
                    <span className="bg-warning-bg text-warning-fg rounded-full px-3 py-1.5 text-[12.5px] font-medium">
                      {t("myInvitations.underReview")}
                    </span>
                  ) : invite.reviewStatus === "rejected" ? (
                    <span className="bg-error-bg text-error rounded-full px-3 py-1.5 text-[12.5px] font-medium">
                      {t("myInvitations.rejected")}
                    </span>
                  ) : (
                    <Button
                      variant="primary"
                      onClick={() => {
                        setActive(invite);
                        setPhase("disclosing");
                      }}
                    >
                      {t("myInvitations.accept")}
                    </Button>
                  )}
                </div>
              </Card>
            ))}
          </div>
        )}
      </div>
    </>
  );
}

function InvitationsShell({
  children,
}: {
  children: React.ReactNode;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <>
      <TopBar
        title={t("myInvitations.title")}
        subtitle={t("myInvitations.subtitle")}
      />
      <div className="flex justify-center p-8">
        <Card className="w-full max-w-md p-8">{children}</Card>
      </div>
    </>
  );
}
