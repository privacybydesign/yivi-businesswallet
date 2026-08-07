import { useEffect, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import { SIGNING_STATUS, downloadSignedDocument } from "../api/signing";
import {
  useCreateSigningRequestMutation,
  useInvalidateSigningCredential,
  useLinkSigningCredentialMutation,
  useSigningCredentialQuery,
  useSigningRequestQuery,
} from "../api/signing.queries";
import { toast } from "../lib/toast";
import { Button, Card, TopBar } from "../ui";

const CONFLICT_STATUS = 409;
const LABEL = "text-ink-soft text-[12px] font-semibold";

// The backend's own message for a rejected start (its APIError), or the transport
// error when there is none. Mirrors the pattern used by the other settings screens.
function startError(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    const code =
      error.body &&
      typeof error.body === "object" &&
      "code" in error.body &&
      typeof error.body.code === "string"
        ? error.body.code
        : "";
    if (code === "not_configured") return t("signing.notConfigured");
    if (code === "no_credential") return t("signing.noCredentialError");
  }
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "message" in error.body &&
    typeof error.body.message === "string"
  ) {
    return t("signing.startError", { message: error.body.message });
  }
  return t("signing.startError", { message: error.message });
}

export default function Signing(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const [searchParams, setSearchParams] = useSearchParams();
  const credential = useSigningCredentialQuery(slug);
  const invalidateCredential = useInvalidateSigningCredential(slug);
  const link = useLinkSigningCredentialMutation(slug);
  const create = useCreateSigningRequestMutation(slug);

  // The signing request to track after returning from the wallet ceremony is
  // taken straight from the URL (?request=<id>, appended by the callback), so we
  // avoid a redundant setState in the effect below.
  const requestId = searchParams.get("request");
  const request = useSigningRequestQuery(slug, requestId);

  const [file, setFile] = useState<File | null>(null);

  // The callback also appends ?link=ok|failed for the credential-link ceremony.
  // Announce it once, then strip the flag so a refresh does not re-fire the toast.
  const linkResult = searchParams.get("link");
  useEffect(() => {
    if (!linkResult) return;
    if (linkResult === "ok") {
      toast.success(t("signing.linkedToast"));
      invalidateCredential();
    } else {
      toast.error(t("signing.linkFailedToast"));
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("link");
        return next;
      },
      { replace: true },
    );
  }, [linkResult, t, invalidateCredential, setSearchParams]);

  const isLinked = credential.data != null;

  const onLink = (): void => {
    link.mutate(undefined, {
      onSuccess: (start) => {
        window.location.assign(start.authorizeUrl);
      },
      onError: (error) => toast.error(startError(error, t)),
    });
  };

  const onSign = (): void => {
    if (!file) return;
    create.mutate(file, {
      onSuccess: (start) => {
        window.location.assign(start.authorizeUrl);
      },
      onError: (error) => toast.error(startError(error, t)),
    });
  };

  const onDownload = (): void => {
    if (!request.data) return;
    void downloadSignedDocument(slug, request.data.id, request.data.filename)
      .then(() => toast.success(t("signing.downloadedToast")))
      .catch(() => toast.error(t("signing.downloadError")));
  };

  return (
    <>
      <TopBar title={t("signing.title")} subtitle={t("signing.subtitle")} />

      <div className="flex max-w-2xl flex-col gap-6">
        {/* Signing credential */}
        <Card className="p-7">
          <h2 className="text-ink text-[15px] font-semibold">
            {t("signing.credentialTitle")}
          </h2>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t("signing.credentialDescription")}
          </p>

          {credential.isPending ? (
            <p className="text-ink-soft mt-4 text-[13px]">
              {t("common.loading")}
            </p>
          ) : credential.isError ? (
            <p className="text-error mt-4 text-[13px]">
              {t("signing.credentialLoadError")}
            </p>
          ) : credential.data != null ? (
            <dl className="mt-4 flex flex-col gap-2">
              <div>
                <dt className={LABEL}>{t("signing.credentialIdLabel")}</dt>
                <dd className="text-ink font-mono text-[12px] break-all">
                  {credential.data.credentialId}
                </dd>
              </div>
              <div>
                <dt className={LABEL}>{t("signing.keyAlgoLabel")}</dt>
                <dd className="text-ink text-[13px]">
                  {credential.data.keyAlgo}
                </dd>
              </div>
            </dl>
          ) : (
            <p className="text-ink-soft mt-4 text-[13px]">
              {t("signing.notLinked")}
            </p>
          )}

          <div className="mt-5">
            <Button type="button" onClick={onLink} loading={link.isPending}>
              {isLinked ? t("signing.relinkButton") : t("signing.linkButton")}
            </Button>
          </div>
          <p className="text-ink-soft mt-3 text-[12px]">
            {t("signing.walletHint")}
          </p>
        </Card>

        {/* Sign a document */}
        <Card className="p-7">
          <h2 className="text-ink text-[15px] font-semibold">
            {t("signing.signTitle")}
          </h2>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t("signing.signDescription")}
          </p>

          {!isLinked && (
            <p className="text-ink-soft mt-4 text-[13px]">
              {t("signing.signNeedsCredential")}
            </p>
          )}

          <div className="mt-4 flex flex-col gap-3">
            <input
              type="file"
              accept="application/pdf"
              disabled={!isLinked}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
              className="text-ink file:bg-surface-2 text-[13px] file:mr-3 file:rounded-md file:border-0 file:px-3 file:py-1.5 file:text-[13px] file:font-medium disabled:opacity-50"
            />
            <div>
              <Button
                type="button"
                onClick={onSign}
                loading={create.isPending}
                disabled={!isLinked || !file}
              >
                {t("signing.signButton")}
              </Button>
            </div>
          </div>
        </Card>

        {/* Active signing request */}
        {requestId && (
          <Card className="p-7">
            <h2 className="text-ink text-[15px] font-semibold">
              {t("signing.requestTitle")}
            </h2>
            {request.isPending ? (
              <p className="text-ink-soft mt-3 text-[13px]">
                {t("common.loading")}
              </p>
            ) : request.isError ? (
              <p className="text-error mt-3 text-[13px]">
                {t("signing.requestLoadError")}
              </p>
            ) : request.data.status === SIGNING_STATUS.completed ? (
              <div className="mt-3 flex flex-col gap-3">
                <p className="text-ink text-[13px]">
                  {t("signing.requestCompleted", {
                    filename: request.data.filename,
                  })}
                </p>
                <div>
                  <Button type="button" onClick={onDownload}>
                    {t("signing.downloadButton")}
                  </Button>
                </div>
              </div>
            ) : request.data.status === SIGNING_STATUS.failed ? (
              <p className="text-error mt-3 text-[13px]">
                {t("signing.requestFailed", {
                  reason:
                    request.data.error || t("signing.requestFailedGeneric"),
                })}
              </p>
            ) : (
              <p className="text-ink-soft mt-3 text-[13px]">
                {t("signing.requestPending")}
              </p>
            )}
          </Card>
        )}
      </div>
    </>
  );
}
