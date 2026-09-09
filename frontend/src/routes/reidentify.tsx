import * as React from "react";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { completeReidentify, reidentifySessionUrl } from "../api/reidentify";
import { useReidentifyPreviewQuery } from "../api/reidentify.queries";
import { reidentifyError } from "../lib/reidentify-error";
import type { ReidentifyErrorContent } from "../lib/reidentify-error";
import { Avatar, Button, Card, IdentityDisclosure, Logo, Outcome } from "../ui";

type Phase = "preview" | "disclosing" | "completing" | "done" | "error";

// The member-side re-identification page. It is public and keyed by the token
// from the reminder e-mail, an admin's request, or the in-app banner — the same
// shape as the invitation-accept page, because it is the same disclosure with a
// membership that already exists.
export default function Reidentify(): React.JSX.Element {
  const { t } = useTranslation();
  const { token } = useParams();
  const navigate = useNavigate();
  const reidentifyToken = token ?? "";
  const preview = useReidentifyPreviewQuery(reidentifyToken);
  const [phase, setPhase] = useState<Phase>("preview");
  const [errorContent, setErrorContent] = useState<ReidentifyErrorContent>(() =>
    reidentifyError(null, t),
  );

  const orgName = preview.data?.organizationName ?? "";
  const loadError = reidentifyError(preview.error, t);

  const fail = (error: unknown): void => {
    setErrorContent(reidentifyError(error, t));
    setPhase("error");
  };

  const onToken = (disclosureToken: string): void => {
    setPhase("completing");
    completeReidentify(reidentifyToken, disclosureToken)
      .then(() => setPhase("done"))
      .catch((error: unknown) => fail(error));
  };

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {preview.isPending ? (
          <p className="text-ink-soft mt-6 text-center text-[14px]">
            {t("reidentify.loading")}
          </p>
        ) : preview.isError ? (
          <Outcome
            tone="error"
            icon="warning"
            title={loadError.title}
            message={loadError.body}
          />
        ) : phase === "done" ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("reidentify.done")}
            message={t("reidentify.doneHint", { org: orgName })}
            action={
              <Button
                variant="primary"
                onClick={() =>
                  void navigate(`/${preview.data.organizationSlug}`)
                }
              >
                {t("reidentify.goToApp")}
              </Button>
            }
          />
        ) : phase === "error" ? (
          <Outcome
            tone="error"
            icon="warning"
            title={errorContent.title}
            message={errorContent.body}
            action={
              <Button variant="secondary" onClick={() => setPhase("preview")}>
                {t("reidentify.retry")}
              </Button>
            }
          />
        ) : phase === "disclosing" || phase === "completing" ? (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {t("reidentify.title")}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("reidentify.scanPrompt", { org: orgName })}
            </p>
            <div className="mt-6 flex justify-center">
              <IdentityDisclosure
                sessionUrl={reidentifySessionUrl(reidentifyToken)}
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
                {t("reidentify.title")}
              </h1>
              <p className="text-ink-soft mt-1 text-[14px]">
                {t("reidentify.intro", { org: orgName })}
              </p>
            </div>

            <div className="rounded-yivi bg-surface-2 mt-6 px-4 py-3 text-center text-[13.5px]">
              <div className="text-ink-soft">
                {t("reidentify.forEmail", { email: preview.data.email })}
              </div>
            </div>

            <Button
              variant="primary"
              className="mt-6 w-full"
              onClick={() => setPhase("disclosing")}
            >
              {t("reidentify.start")}
            </Button>
          </>
        )}
      </Card>
    </div>
  );
}
