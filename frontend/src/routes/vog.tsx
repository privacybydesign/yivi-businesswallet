import * as React from "react";
import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useCompleteIdentityVogCredentialMutation,
  useCompleteOwnIdentityMutation,
  useCompleteVogCredentialMutation,
  useOrganizationQuery,
  useUploadVogMutation,
} from "../api/organization.queries";
import type { UploadVogResult } from "../api/organization";
import {
  identitySessionUrl,
  identityVogCredentialSessionUrl,
  vogCredentialSessionUrl,
} from "../api/organization";
import { accessMessage } from "../lib/access-message";
import { errorCode } from "../lib/api-error";
import { reidentifyError } from "../lib/reidentify-error";
import { vogResultMessage } from "../lib/screening-status";
import { isIdentityRejection, needsIdentityFirst } from "../lib/vog-page";
import { Button, Card, IdentityDisclosure, TopBar } from "../ui";

// Which wallet session, if any, is showing its QR right now - one at a time.
type Disclosing = "none" | "identity" | "identityVog" | "vog";

// The member's own VOG screening page: upload a PDF (validated live against
// validatie.nl) or, if the org opted in, disclose the pbdf.vog credential from
// the wallet. A member who never identified (no date of birth on file) first
// confirms their identity here - in-app, or in the same wallet session as the
// VOG credential - instead of being sent away. Unlike re-identification this
// is an ordinary in-app page, not a bearer-token link: a member being screened
// already has an account and is simply signed in.
export default function Vog(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const uploadVog = useUploadVogMutation(slug);
  const completeCredential = useCompleteVogCredentialMutation(slug);
  const completeIdentity = useCompleteOwnIdentityMutation(slug);
  const completeIdentityVog = useCompleteIdentityVogCredentialMutation(slug);
  const [disclosing, setDisclosing] = useState<Disclosing>("none");
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

  const orgName = org.data.name;
  const vog = org.data.vog;
  const needsIdentity = needsIdentityFirst(vog, [
    errorCode(uploadVog.error),
    errorCode(completeCredential.error),
  ]);

  const onVogToken = (disclosureToken: string): void => {
    setDisclosing("none");
    completeCredential.mutate(disclosureToken);
  };
  const onIdentityToken = (disclosureToken: string): void => {
    setDisclosing("none");
    completeIdentity.mutate(disclosureToken);
  };
  const onIdentityVogToken = (disclosureToken: string): void => {
    setDisclosing("none");
    completeIdentityVog.mutate(disclosureToken);
  };

  // The QR closes as soon as the wallet hands over its token, so the button
  // it replaced is back on screen - showing its loading state - while the
  // disclosure completes. Starting another session meanwhile is blocked.
  const busy =
    disclosing !== "none" ||
    completeCredential.isPending ||
    completeIdentity.isPending ||
    completeIdentityVog.isPending;

  const outcomeLine = (outcome: UploadVogResult): React.JSX.Element => (
    <p
      role="status"
      className={`mt-3 text-[13.5px] ${outcome.result === "valid" ? "text-success" : "text-error"}`}
    >
      {vogResultMessage(outcome.result, outcome.rejectionReason, orgName, t)}
    </p>
  );

  // The identity half of a combined disclosure fails the way re-identification
  // does; anything else is the generic VOG failure line.
  const identityVogErrorLine = (error: Error): React.JSX.Element => {
    const code = errorCode(error);
    if (isIdentityRejection(code)) {
      const content = reidentifyError(error, t);
      return (
        <p role="alert" className="text-error mt-3 text-[13.5px]">
          <span className="font-semibold">{content.title}</span> {content.body}
        </p>
      );
    }
    return (
      <p role="alert" className="text-error mt-3 text-[13.5px]">
        {t("vog.credential.error", { message: error.message })}
      </p>
    );
  };

  return (
    <>
      <TopBar
        title={t("vog.title")}
        subtitle={t("vog.subtitle", { org: orgName })}
      />
      <div className="mx-auto flex max-w-xl flex-col gap-6 p-8">
        {completeIdentityVog.data && !needsIdentity && (
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("vog.identity.combinedDone")}
            </h2>
            {outcomeLine(completeIdentityVog.data)}
          </Card>
        )}

        {needsIdentity ? (
          <>
            <Card className="p-6">
              <h2 className="text-[16px] font-semibold">
                {t("vog.identity.heading")}
              </h2>
              <p className="text-ink-soft mt-2 text-[14px]">
                {t("vog.identity.hint", { org: orgName })}
              </p>
              {disclosing === "identity" ? (
                <div className="mt-4 flex justify-center">
                  <IdentityDisclosure
                    sessionUrl={identitySessionUrl(slug)}
                    onToken={onIdentityToken}
                    onAborted={() => setDisclosing("none")}
                  />
                </div>
              ) : (
                <Button
                  variant="primary"
                  className="mt-4"
                  loading={completeIdentity.isPending}
                  disabled={busy}
                  onClick={() => setDisclosing("identity")}
                >
                  {completeIdentity.isPending
                    ? t("vog.identity.completing")
                    : t("vog.identity.start")}
                </Button>
              )}
              {completeIdentity.isError && (
                <p role="alert" className="text-error mt-3 text-[13.5px]">
                  <span className="font-semibold">
                    {reidentifyError(completeIdentity.error, t).title}
                  </span>{" "}
                  {reidentifyError(completeIdentity.error, t).body}
                </p>
              )}
            </Card>

            {vog?.acceptCredential && (
              <Card className="p-6">
                <h2 className="text-[16px] font-semibold">
                  {t("vog.identity.combinedHeading")}
                </h2>
                <p className="text-ink-soft mt-2 text-[14px]">
                  {t("vog.identity.combinedHint", { org: orgName })}
                </p>
                {disclosing === "identityVog" ? (
                  <div className="mt-4 flex justify-center">
                    <IdentityDisclosure
                      sessionUrl={identityVogCredentialSessionUrl(slug)}
                      onToken={onIdentityVogToken}
                      onAborted={() => setDisclosing("none")}
                    />
                  </div>
                ) : (
                  <Button
                    variant="secondary"
                    className="mt-4"
                    loading={completeIdentityVog.isPending}
                    disabled={busy}
                    onClick={() => setDisclosing("identityVog")}
                  >
                    {completeIdentityVog.isPending
                      ? t("vog.identity.completing")
                      : t("vog.identity.combinedStart")}
                  </Button>
                )}
                {completeIdentityVog.data &&
                  outcomeLine(completeIdentityVog.data)}
                {completeIdentityVog.isError &&
                  identityVogErrorLine(completeIdentityVog.error)}
              </Card>
            )}
          </>
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
              {uploadVog.data && outcomeLine(uploadVog.data)}
              {uploadVog.isError && (
                <p role="alert" className="text-error mt-3 text-[13.5px]">
                  {t("vog.upload.error", { message: uploadVog.error.message })}
                </p>
              )}
            </Card>

            {vog?.acceptCredential && (
              <Card className="p-6">
                <h2 className="text-[16px] font-semibold">
                  {t("vog.credential.heading")}
                </h2>
                <p className="text-ink-soft mt-2 text-[14px]">
                  {t("vog.credential.hint", { org: orgName })}
                </p>
                {disclosing === "vog" ? (
                  <div className="mt-4 flex justify-center">
                    <IdentityDisclosure
                      sessionUrl={vogCredentialSessionUrl(slug)}
                      onToken={onVogToken}
                      onAborted={() => setDisclosing("none")}
                    />
                  </div>
                ) : (
                  <Button
                    variant="secondary"
                    className="mt-4"
                    loading={completeCredential.isPending}
                    disabled={busy}
                    onClick={() => setDisclosing("vog")}
                  >
                    {t("vog.credential.start")}
                  </Button>
                )}
                {completeCredential.data &&
                  outcomeLine(completeCredential.data)}
                {completeCredential.isError && (
                  <p role="alert" className="text-error mt-3 text-[13.5px]">
                    {t("vog.credential.error", {
                      message: completeCredential.error.message,
                    })}
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
