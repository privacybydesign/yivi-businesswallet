import * as React from "react";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { UploadVogResult } from "../api/organization";
import {
  vogCredentialSessionUrl,
  vogIdentityCredentialSessionUrl,
  vogIdentitySessionUrl,
} from "../api/vog-link";
import {
  useCompleteVogCredentialMutation,
  useCompleteVogIdentityCredentialMutation,
  useCompleteVogIdentityMutation,
  useUploadVogByLinkMutation,
  useVogPreviewQuery,
} from "../api/vog-link.queries";
import { errorCode } from "../lib/api-error";
import { reidentifyError } from "../lib/reidentify-error";
import { vogResultMessage } from "../lib/screening-status";
import { isIdentityRejection, needsIdentityFirst } from "../lib/vog-page";
import { Avatar, Button, Card, IdentityDisclosure, Logo, Outcome } from "../ui";

// Which wallet session, if any, is showing its QR right now - one at a time.
type Disclosing = "none" | "identity" | "identityVog" | "vog";

const VALID_RESULT = "valid";
const LINK_NOT_FOUND = "vog_link_not_found";

// The member's VOG submission page. It is public and keyed by the token from
// a request or reminder e-mail, or the dashboard banner - the same shape as a
// credential claim or re-identification link, so the member submits from
// whatever device they opened the mail on, without signing in. Upload a PDF
// (validated live against validatie.nl) or, if the org opted in, disclose the
// pbdf.vog credential. A member who never identified (no date of birth on
// file) confirms their identity here first, or in the same wallet session as
// the VOG credential.
export default function VogSubmit(): React.JSX.Element {
  const { t } = useTranslation();
  const { token } = useParams();
  const navigate = useNavigate();
  const vogToken = token ?? "";
  const preview = useVogPreviewQuery(vogToken);
  const uploadVog = useUploadVogByLinkMutation(vogToken);
  const completeCredential = useCompleteVogCredentialMutation(vogToken);
  const completeIdentity = useCompleteVogIdentityMutation(vogToken);
  const completeIdentityVog =
    useCompleteVogIdentityCredentialMutation(vogToken);
  const [disclosing, setDisclosing] = useState<Disclosing>("none");
  const fileInput = React.useRef<HTMLInputElement>(null);

  const orgName = preview.data?.organizationName ?? "";

  // A valid result retires the link server-side, so success is read from the
  // mutation that produced it rather than from a refetched preview.
  const passed = [
    uploadVog.data,
    completeCredential.data,
    completeIdentityVog.data,
  ].some((outcome) => outcome?.result === VALID_RESULT);

  const needsIdentity = needsIdentityFirst(preview.data, [
    errorCode(uploadVog.error),
    errorCode(completeCredential.error),
  ]);

  const onVogToken = (disclosureToken: string): void => {
    setDisclosing("none");
    completeCredential.mutate(disclosureToken);
  };
  const onIdentityToken = (disclosureToken: string): void => {
    setDisclosing("none");
    completeIdentity.mutate(disclosureToken, {
      // An earlier "no date of birth" refusal would otherwise keep asking
      // for identity after it is on file.
      onSuccess: () => {
        uploadVog.reset();
        completeCredential.reset();
      },
    });
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
    uploadVog.isPending ||
    completeCredential.isPending ||
    completeIdentity.isPending ||
    completeIdentityVog.isPending;

  const outcomeLine = (outcome: UploadVogResult): React.JSX.Element => (
    <p role="status" className="text-error mt-3 text-[13.5px]">
      {vogResultMessage(outcome.result, outcome.rejectionReason, orgName, t)}
    </p>
  );

  const errorLine = (error: Error): React.JSX.Element => {
    // The identity half of a disclosure fails the way re-identification does;
    // anything else is the generic VOG failure line.
    if (isIdentityRejection(errorCode(error))) {
      const content = reidentifyError(error, t);
      return (
        <p role="alert" className="text-error mt-3 text-[13.5px]">
          <span className="font-semibold">{content.title}</span> {content.body}
        </p>
      );
    }
    return (
      <p role="alert" className="text-error mt-3 text-[13.5px]">
        {t("vog.submitError", { message: error.message })}
      </p>
    );
  };

  const section = (
    heading: string,
    hint: string,
    body: React.ReactNode,
  ): React.JSX.Element => (
    <section className="border-line border-t pt-5">
      <h2 className="text-[15px] font-semibold">{heading}</h2>
      <p className="text-ink-soft mt-1 text-[13.5px]">{hint}</p>
      {body}
    </section>
  );

  const disclosureSection = (
    kind: Exclude<Disclosing, "none">,
    sessionUrl: string,
    onToken: (disclosureToken: string) => void,
    heading: string,
    hint: string,
    startLabel: string,
    mutation: {
      isPending: boolean;
      isError: boolean;
      error: Error | null;
      data?: UploadVogResult | void;
    },
    variant: "primary" | "secondary",
  ): React.JSX.Element =>
    section(
      heading,
      hint,
      <>
        {disclosing === kind ? (
          <div className="mt-4 flex justify-center">
            <IdentityDisclosure
              sessionUrl={sessionUrl}
              onToken={onToken}
              onAborted={() => setDisclosing("none")}
            />
          </div>
        ) : (
          <Button
            variant={variant}
            className="mt-4 w-full"
            loading={mutation.isPending}
            disabled={busy}
            onClick={() => setDisclosing(kind)}
          >
            {mutation.isPending ? t("vog.completing") : startLabel}
          </Button>
        )}
        {mutation.data && outcomeLine(mutation.data)}
        {mutation.isError && mutation.error && errorLine(mutation.error)}
      </>,
    );

  const renderBody = (): React.JSX.Element => {
    if (preview.isPending) {
      return (
        <p className="text-ink-soft mt-6 text-center text-[14px]">
          {t("vog.loading")}
        </p>
      );
    }
    if (preview.isError) {
      const notFound = errorCode(preview.error) === LINK_NOT_FOUND;
      return (
        <Outcome
          tone="error"
          icon="warning"
          title={notFound ? t("vog.linkNotFoundTitle") : t("vog.errorTitle")}
          message={
            notFound
              ? t("vog.linkNotFoundBody")
              : t("vog.errorBody", { message: preview.error.message })
          }
        />
      );
    }
    if (passed) {
      return (
        <Outcome
          tone="success"
          icon="valid"
          title={t("vog.result.valid")}
          message={t("vog.doneHint", { org: orgName })}
          action={
            <Button
              variant="secondary"
              onClick={() => void navigate(`/${preview.data.organizationSlug}`)}
            >
              {t("vog.goToApp")}
            </Button>
          }
        />
      );
    }

    const acceptCredential = preview.data.acceptCredential;
    return (
      <>
        <div className="mt-6 flex flex-col items-center text-center">
          <Avatar name={orgName} tone="rose" size="lg" />
          <h1 className="text-ink mt-4 text-[22px] font-bold">
            {t("vog.title")}
          </h1>
          <p className="text-ink-soft mt-1 text-[14px]">
            {t("vog.subtitle", { org: orgName })}
          </p>
        </div>

        <div className="rounded-yivi bg-surface-2 text-ink-soft mt-6 px-4 py-3 text-center text-[13.5px]">
          {t("vog.forEmail", { email: preview.data.email })}
        </div>

        <div className="mt-6 flex flex-col gap-5">
          {/* A combined disclosure whose VOG half did not pass still put the
              identity on file, so the page has moved on to the upload - keep
              saying what happened to the VOG. */}
          {completeIdentityVog.data && !needsIdentity && (
            <div className="rounded-yivi bg-surface-2 px-4 py-3">
              <p className="text-ink text-[13.5px] font-semibold">
                {t("vog.identity.combinedDone")}
              </p>
              {outcomeLine(completeIdentityVog.data)}
            </div>
          )}
          {needsIdentity ? (
            <>
              {disclosureSection(
                "identity",
                vogIdentitySessionUrl(vogToken),
                onIdentityToken,
                t("vog.identity.heading"),
                t("vog.identity.hint", { org: orgName }),
                t("vog.identity.start"),
                completeIdentity,
                "primary",
              )}
              {acceptCredential &&
                disclosureSection(
                  "identityVog",
                  vogIdentityCredentialSessionUrl(vogToken),
                  onIdentityVogToken,
                  t("vog.identity.combinedHeading"),
                  t("vog.identity.combinedHint", { org: orgName }),
                  t("vog.identity.combinedStart"),
                  completeIdentityVog,
                  "secondary",
                )}
            </>
          ) : (
            <>
              {section(
                t("vog.upload.heading"),
                t("vog.upload.hint"),
                <>
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
                    className="mt-4 w-full"
                    loading={uploadVog.isPending}
                    disabled={busy}
                    onClick={() => fileInput.current?.click()}
                  >
                    {uploadVog.isPending
                      ? t("vog.upload.uploading")
                      : t("vog.upload.chooseFile")}
                  </Button>
                  {uploadVog.data && outcomeLine(uploadVog.data)}
                  {uploadVog.isError && errorLine(uploadVog.error)}
                </>,
              )}
              {acceptCredential &&
                disclosureSection(
                  "vog",
                  vogCredentialSessionUrl(vogToken),
                  onVogToken,
                  t("vog.credential.heading"),
                  t("vog.credential.hint", { org: orgName }),
                  t("vog.credential.start"),
                  completeCredential,
                  "secondary",
                )}
            </>
          )}
        </div>
      </>
    );
  };

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>
        {renderBody()}
      </Card>
    </div>
  );
}
