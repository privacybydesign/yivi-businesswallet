import * as React from "react";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useCompleteVogCredentialMutation,
  useOrganizationQuery,
  useUploadVogMutation,
} from "../api/organization.queries";
import { vogCredentialSessionUrl } from "../api/organization";
import { accessMessage } from "../lib/access-message";
import { errorCode } from "../lib/api-error";
import { screeningResultLabel } from "../lib/screening-status";
import { Button, Card, IdentityDisclosure, TopBar } from "../ui";

type CredentialPhase = "idle" | "disclosing" | "completing";

// The member's own VOG screening page: upload a PDF (validated live against
// validatie.nl) or, if the org opted in, disclose the pbdf.vog credential from
// the wallet. Unlike re-identification this is an ordinary in-app page, not a
// bearer-token link - a member being screened already has an account and is
// simply signed in.
export default function Vog(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const uploadVog = useUploadVogMutation(slug);
  const completeCredential = useCompleteVogCredentialMutation(slug);
  const [credentialPhase, setCredentialPhase] =
    useState<CredentialPhase>("idle");
  const fileInput = React.useRef<HTMLInputElement>(null);

  if (org.isError) {
    return (
      <>
        <TopBar title={t("vog.title")} />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        </div>
      </>
    );
  }
  if (org.isPending) {
    return (
      <>
        <TopBar title={t("vog.title")} />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
          </Card>
        </div>
      </>
    );
  }

  const vog = org.data.vog;
  const noDateOfBirth = errorCode(uploadVog.error) === "no_date_of_birth";

  const onToken = (disclosureToken: string): void => {
    setCredentialPhase("completing");
    completeCredential.mutate(disclosureToken, {
      onSettled: () => setCredentialPhase("idle"),
    });
  };

  return (
    <>
      <TopBar
        title={t("vog.title")}
        subtitle={t("vog.subtitle", { org: org.data.name })}
      />
      <div className="mx-auto flex max-w-xl flex-col gap-6 p-8">
        {noDateOfBirth ? (
          <Card className="p-6">
            <p className="text-ink text-[14px]">{t("vog.noDateOfBirth")}</p>
            <Button
              variant="primary"
              className="mt-4"
              onClick={() => void navigate(`/${slug}`)}
            >
              {t("vog.goToReidentify")}
            </Button>
          </Card>
        ) : (
          <>
            <Card className="p-6">
              <h2 className="text-[16px] font-semibold">
                {t("vog.upload.heading")}
              </h2>
              <p className="text-ink-soft mt-2 text-[14px]">
                {t("vog.upload.hint")}
              </p>
              <input
                ref={fileInput}
                type="file"
                accept="application/pdf"
                className="hidden"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  event.target.value = "";
                  if (file) uploadVog.mutate(file);
                }}
              />
              <Button
                variant="primary"
                className="mt-4"
                loading={uploadVog.isPending}
                onClick={() => fileInput.current?.click()}
              >
                {uploadVog.isPending
                  ? t("vog.upload.uploading")
                  : t("vog.upload.chooseFile")}
              </Button>
              {uploadVog.data && (
                <p
                  role="status"
                  className={`mt-3 text-[13.5px] ${uploadVog.data.result === "valid" ? "text-success" : "text-error"}`}
                >
                  {screeningResultLabel(uploadVog.data.result, t)}
                </p>
              )}
            </Card>

            {vog?.acceptCredential && (
              <Card className="p-6">
                <h2 className="text-[16px] font-semibold">
                  {t("vog.credential.heading")}
                </h2>
                <p className="text-ink-soft mt-2 text-[14px]">
                  {t("vog.credential.hint", { org: org.data.name })}
                </p>
                {credentialPhase === "idle" ? (
                  <Button
                    variant="secondary"
                    className="mt-4"
                    onClick={() => setCredentialPhase("disclosing")}
                  >
                    {t("vog.credential.start")}
                  </Button>
                ) : (
                  <div className="mt-4 flex justify-center">
                    <IdentityDisclosure
                      sessionUrl={vogCredentialSessionUrl(slug)}
                      onToken={onToken}
                      onAborted={() => setCredentialPhase("idle")}
                    />
                  </div>
                )}
                {completeCredential.data && (
                  <p
                    role="status"
                    className={`mt-3 text-[13.5px] ${completeCredential.data.result === "valid" ? "text-success" : "text-error"}`}
                  >
                    {screeningResultLabel(completeCredential.data.result, t)}
                  </p>
                )}
              </Card>
            )}
          </>
        )}
      </div>
    </>
  );
}
