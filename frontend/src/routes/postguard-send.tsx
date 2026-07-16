import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useSendPostguardFileMutation } from "../api/postguard.queries";
import { formatBytes } from "../lib/format-bytes";
import { ApiError } from "../api/http";
import { Button, Card, Icon, TopBar } from "../ui";
import * as React from "react";

const FORM_ID = "postguard-send-form";
const CONFLICT_STATUS = 409;
const PAYLOAD_TOO_LARGE_STATUS = 413;
// Plausible address check only; the PostGuard PKG is the authority.
const ADDRESS_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const EXPIRY_OPTIONS = ["7d", "30d", "never"] as const;
type Expiry = (typeof EXPIRY_OPTIONS)[number];

const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

function control(hasError: boolean): string {
  return [CONTROL, hasError ? CONTROL_ERR : CONTROL_OK].join(" ");
}

function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function sendErrorMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError) {
    if (
      error.status === CONFLICT_STATUS &&
      errorCode(error) === "api_key_not_set"
    ) {
      return t("postguard.send.noApiKey");
    }
    if (errorCode(error) === "postguard_not_configured") {
      return t("postguard.send.notConfigured");
    }
    if (error.status === PAYLOAD_TOO_LARGE_STATUS) {
      return t("postguard.send.tooLarge");
    }
    if (errorCode(error) === "postguard_upstream") {
      return t("postguard.send.upstream");
    }
  }
  return t("postguard.send.error", { message: error.message });
}

export default function PostguardSend(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const send = useSendPostguardFileMutation(slug);

  const [files, setFiles] = useState<File[]>([]);
  const [recipients, setRecipients] = useState<string[]>([""]);
  const [expiry, setExpiry] = useState<Expiry>("7d");
  const [message, setMessage] = useState("");
  const [notify, setNotify] = useState(true);
  const [attempted, setAttempted] = useState(false);

  const backToList = (): void => void navigate(`/${slug}/postguard`);

  const trimmedRecipients = recipients
    .map((r) => r.trim())
    .filter((r) => r !== "");
  const validRecipients = trimmedRecipients.filter((r) =>
    ADDRESS_PATTERN.test(r),
  );
  const filesError = attempted && files.length === 0;
  const recipientsError =
    attempted &&
    (validRecipients.length === 0 ||
      validRecipients.length !== trimmedRecipients.length);

  function updateRecipient(index: number, value: string): void {
    setRecipients((prev) => prev.map((r, i) => (i === index ? value : r)));
  }

  function addRecipient(): void {
    setRecipients((prev) => [...prev, ""]);
  }

  function removeRecipient(index: number): void {
    setRecipients((prev) =>
      prev.length === 1 ? prev : prev.filter((_, i) => i !== index),
    );
  }

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (send.isPending) {
      return;
    }
    if (
      files.length === 0 ||
      validRecipients.length === 0 ||
      validRecipients.length !== trimmedRecipients.length
    ) {
      return;
    }
    send.mutate(
      {
        files,
        recipients: validRecipients,
        notify,
        message: message.trim() || undefined,
        expiresAfter: expiry,
      },
      { onSuccess: backToList },
    );
  }

  return (
    <>
      <TopBar
        title={t("postguard.send.title")}
        subtitle={t("postguard.send.subtitle")}
        actions={
          <>
            <Button variant="secondary" onClick={backToList}>
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form={FORM_ID}
              icon="lock"
              disabled={send.isPending}
            >
              {send.isPending
                ? t("postguard.send.sending")
                : t("postguard.send.submit")}
            </Button>
          </>
        }
      />

      <form
        id={FORM_ID}
        onSubmit={handleSubmit}
        noValidate
        className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]"
      >
        <Card className="flex flex-col gap-5 p-5">
          {/* File */}
          <div className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>{t("postguard.send.fileLabel")}</span>
            <label
              className={[
                "rounded-yivi flex cursor-pointer flex-col items-center justify-center border border-dashed px-4 py-6 text-center transition-colors",
                filesError
                  ? "border-error bg-error-bg/40"
                  : "border-line-strong hover:bg-surface-3",
              ].join(" ")}
            >
              <input
                type="file"
                multiple
                className="sr-only"
                onChange={(event) =>
                  setFiles(Array.from(event.target.files ?? []))
                }
              />
              <Icon name="add" size={20} className="text-muted" />
              <span className="text-ink mt-1.5 text-[13px] font-semibold">
                {t("postguard.send.fileCta")}
              </span>
              <span className="text-ink-soft mt-0.5 text-[12px]">
                {t("postguard.send.fileHint")}
              </span>
            </label>
            {files.length > 0 && (
              <ul className="flex flex-col gap-1">
                {files.map((file, index) => (
                  <li
                    key={`${file.name}-${index}`}
                    className="rounded-yivi border-line bg-surface-2 flex items-center justify-between gap-2 border px-3 py-1.5 text-[13px]"
                  >
                    <span className="truncate">{file.name}</span>
                    <span className="text-ink-soft shrink-0 font-mono text-[12px]">
                      {formatBytes(file.size)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {filesError && (
              <span role="alert" className="text-error text-[12px]">
                {t("postguard.send.fileRequired")}
              </span>
            )}
          </div>

          {/* Recipients */}
          <div className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL}>
              {t("postguard.send.recipientsLabel")}
            </span>
            <div className="flex flex-col gap-2">
              {recipients.map((recipient, index) => (
                <div key={index} className="flex items-center gap-2">
                  <input
                    className={`${control(recipientsError)} h-9`}
                    type="email"
                    value={recipient}
                    onChange={(event) =>
                      updateRecipient(index, event.target.value)
                    }
                    placeholder={t("postguard.send.recipientPlaceholder")}
                    aria-label={t("postguard.send.recipientsLabel")}
                  />
                  {recipients.length > 1 && (
                    <Button
                      variant="ghost"
                      size="sm"
                      icon="delete"
                      iconOnly
                      aria-label={t("postguard.send.removeRecipient")}
                      onClick={() => removeRecipient(index)}
                    />
                  )}
                </div>
              ))}
              <button
                type="button"
                onClick={addRecipient}
                className="text-link hover:text-ink inline-flex w-fit items-center gap-1 text-[13px] font-semibold transition-colors"
              >
                <Icon name="add" size={12} />
                {t("postguard.send.addRecipient")}
              </button>
            </div>
            {recipientsError && (
              <span role="alert" className="text-error text-[12px]">
                {t("postguard.send.recipientsInvalid")}
              </span>
            )}
          </div>

          {/* Expiry */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="postguard-expiry" className={FIELD_LABEL}>
              {t("postguard.send.expiryLabel")}
            </label>
            <select
              id="postguard-expiry"
              className={`${control(false)} h-9`}
              value={expiry}
              onChange={(event) => setExpiry(event.target.value as Expiry)}
            >
              {EXPIRY_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {t(`postguard.send.expiry.${option}`)}
                </option>
              ))}
            </select>
          </div>

          {/* Message */}
          <div className="flex flex-col gap-1.5">
            <label htmlFor="postguard-message" className={FIELD_LABEL}>
              {t("postguard.send.messageLabel")}
            </label>
            <textarea
              id="postguard-message"
              className={`${control(false)} min-h-24 py-2 leading-relaxed`}
              value={message}
              onChange={(event) => setMessage(event.target.value)}
            />
          </div>

          <label className="flex items-center gap-2 text-[13px]">
            <input
              type="checkbox"
              checked={notify}
              onChange={(event) => setNotify(event.target.checked)}
            />
            {t("postguard.send.notify")}
          </label>

          {send.isError && (
            <p
              role="alert"
              className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
            >
              {sendErrorMessage(send.error, t)}
            </p>
          )}
        </Card>

        <Card variant="highlight" className="h-fit p-4">
          <div className="flex items-start gap-2.5">
            <Icon name="info" size={16} className="text-link mt-0.5 shrink-0" />
            <p className="text-ink text-[13px]">{t("postguard.send.note")}</p>
          </div>
        </Card>
      </form>
    </>
  );
}
