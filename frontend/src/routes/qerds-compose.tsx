import { useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useQerdsAddressesQuery,
  useQerdsContactsQuery,
  useSendQerdsMessageMutation,
} from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { formatBytes } from "../lib/qerds";
import { Button, Card, Icon, TopBar } from "../ui";
import * as React from "react";

const FORM_ID = "qerds-compose-form";
const CONFLICT_STATUS = 409;
// Plausible address check only; the provider/directory is the authority.
const ADDRESS_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

// Client-side attachment limits, mirroring the backend handler. The server
// re-enforces them; these are for immediate feedback only.
const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024; // 25 MiB per file
const MAX_ATTACHMENT_TOTAL_BYTES = 50 * 1024 * 1024; // 50 MiB per message
const MAX_ATTACHMENT_COUNT = 20;

const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

function control(hasError: boolean): string {
  return [CONTROL, hasError ? CONTROL_ERR : CONTROL_OK].join(" ");
}

type MessageKey =
  | "qerds.compose.recipientRequired"
  | "qerds.compose.recipientInvalid"
  | "qerds.compose.subjectRequired";

type FieldErrors = {
  recipient?: MessageKey;
  subject?: MessageKey;
};

type Values = {
  recipient: string;
  subject: string;
};

function validate(values: Values): FieldErrors {
  const errors: FieldErrors = {};
  const recipient = values.recipient.trim();
  if (recipient === "") {
    errors.recipient = "qerds.compose.recipientRequired";
  } else if (!ADDRESS_PATTERN.test(recipient)) {
    errors.recipient = "qerds.compose.recipientInvalid";
  }
  if (values.subject.trim() === "") {
    errors.subject = "qerds.compose.subjectRequired";
  }
  return errors;
}

function errorCode(error: ApiError): string | null {
  if (error.body && typeof error.body === "object" && "code" in error.body) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function errorMessage(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    errorCode(error) === "no_sender_address"
  ) {
    return t("qerds.compose.noSenderAddress");
  }
  return t("qerds.compose.error", { message: error.message });
}

function Field({
  id,
  label,
  required,
  error,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  error?: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className={FIELD_LABEL}>
        {label}
        {required && (
          <span aria-hidden className="text-error ml-0.5">
            *
          </span>
        )}
      </label>
      {children}
      {error && (
        <span
          id={`${id}-error`}
          role="alert"
          className="text-error text-[12px]"
        >
          {error}
        </span>
      )}
    </div>
  );
}

export default function QerdsCompose(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const [searchParams] = useSearchParams();

  const org = useOrganizationQuery(slug);
  const addresses = useQerdsAddressesQuery(slug, !org.isError);
  const contacts = useQerdsContactsQuery(slug, !org.isError);
  const send = useSendQerdsMessageMutation(slug);

  const [sender, setSender] = useState("");
  // Prefill the recipient from a "to" query param (e.g. from a member detail
  // page); read once at mount since compose is a fresh navigation.
  const [recipient, setRecipient] = useState(
    () => searchParams.get("to") ?? "",
  );
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [fileError, setFileError] = useState<string | null>(null);
  const [attempted, setAttempted] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const addressOptions = addresses.data ?? [];
  const defaultAddress = addressOptions.find((address) => address.isDefault);
  // The chosen "from"; falls back to the org default until the user picks one.
  const selectedFrom = sender || defaultAddress?.address || "";
  const backToList = (): void => void navigate(`/${slug}/qerds`);

  const errors = attempted ? validate({ recipient, subject }) : {};

  function addFiles(selected: FileList | null): void {
    if (!selected || selected.length === 0) {
      return;
    }
    const key = (f: File): string => `${f.name}:${f.size}:${f.lastModified}`;
    const seen = new Set(files.map(key));
    const merged = [...files];
    for (const file of Array.from(selected)) {
      if (!seen.has(key(file))) {
        seen.add(key(file));
        merged.push(file);
      }
    }

    if (merged.length > MAX_ATTACHMENT_COUNT) {
      setFileError(
        t("qerds.compose.tooManyAttachments", { max: MAX_ATTACHMENT_COUNT }),
      );
      return;
    }
    if (merged.some((file) => file.size > MAX_ATTACHMENT_BYTES)) {
      setFileError(
        t("qerds.compose.attachmentTooLarge", {
          size: formatBytes(MAX_ATTACHMENT_BYTES),
        }),
      );
      return;
    }
    const total = merged.reduce((sum, file) => sum + file.size, 0);
    if (total > MAX_ATTACHMENT_TOTAL_BYTES) {
      setFileError(
        t("qerds.compose.attachmentsTooLarge", {
          size: formatBytes(MAX_ATTACHMENT_TOTAL_BYTES),
        }),
      );
      return;
    }

    setFileError(null);
    setFiles(merged);
  }

  function removeFile(index: number): void {
    setFiles((current) => current.filter((_, i) => i !== index));
    setFileError(null);
  }

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (send.isPending) {
      return;
    }
    const found = validate({ recipient, subject });
    if (Object.keys(found).length > 0) {
      return;
    }
    send.mutate(
      {
        sender: selectedFrom || undefined,
        recipient: recipient.trim(),
        subject: subject.trim(),
        body,
        attachments: files,
      },
      {
        onSuccess: (message) => void navigate(`/${slug}/qerds/${message.id}`),
      },
    );
  }

  return (
    <>
      <TopBar
        title={t("qerds.compose.title")}
        subtitle={t("qerds.compose.subtitle")}
        actions={
          <>
            <Button variant="secondary" onClick={backToList}>
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form={FORM_ID}
              icon="email"
              disabled={send.isPending}
            >
              {send.isPending
                ? t("qerds.compose.sending")
                : t("qerds.compose.send")}
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
          <Field id="qerds-from" label={t("qerds.compose.from")}>
            {addressOptions.length > 0 ? (
              <select
                id="qerds-from"
                className={`${control(false)} h-9`}
                value={selectedFrom}
                onChange={(event) => setSender(event.target.value)}
              >
                {addressOptions.map((address) => (
                  <option key={address.id} value={address.address}>
                    {address.isDefault
                      ? t("qerds.compose.fromDefaultOption", {
                          address: address.address,
                        })
                      : address.address}
                  </option>
                ))}
              </select>
            ) : (
              <input
                id="qerds-from"
                className={`${control(false)} h-9`}
                value=""
                placeholder={t("qerds.compose.noAddressPlaceholder")}
                readOnly
                disabled
              />
            )}
          </Field>

          <Field
            id="qerds-recipient"
            label={t("qerds.compose.recipient")}
            required
            error={errors.recipient && t(errors.recipient)}
          >
            {contacts.data && contacts.data.length > 0 && (
              // Explicit picker for the interim address book (until the European
              // Digital Directory); the free-text input below still accepts any
              // address.
              <select
                className={`${control(false)} mb-2 h-9`}
                value=""
                onChange={(event) => {
                  if (event.target.value !== "") {
                    setRecipient(event.target.value);
                  }
                }}
                aria-label={t("qerds.compose.chooseContact")}
              >
                <option value="">{t("qerds.compose.chooseContact")}</option>
                {contacts.data.map((contact) => (
                  <option key={contact.id} value={contact.address}>
                    {contact.name} — {contact.address}
                  </option>
                ))}
              </select>
            )}
            <input
              id="qerds-recipient"
              className={`${control(Boolean(errors.recipient))} h-9`}
              value={recipient}
              onChange={(event) => setRecipient(event.target.value)}
              placeholder={t("qerds.compose.recipientPlaceholder")}
              list="qerds-contacts"
              aria-required
              aria-invalid={errors.recipient ? true : undefined}
              aria-describedby={
                errors.recipient ? "qerds-recipient-error" : undefined
              }
              autoFocus
            />
            {/* Interim address book (until the European Digital Directory): pick
                a saved contact or type an address. */}
            <datalist id="qerds-contacts">
              {contacts.data?.map((contact) => (
                <option key={contact.id} value={contact.address}>
                  {contact.name}
                </option>
              ))}
            </datalist>
          </Field>

          <Field
            id="qerds-subject"
            label={t("qerds.compose.subjectLabel")}
            required
            error={errors.subject && t(errors.subject)}
          >
            <input
              id="qerds-subject"
              className={`${control(Boolean(errors.subject))} h-9`}
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
              aria-required
              aria-invalid={errors.subject ? true : undefined}
              aria-describedby={
                errors.subject ? "qerds-subject-error" : undefined
              }
            />
          </Field>

          <Field id="qerds-body" label={t("qerds.compose.bodyLabel")}>
            <textarea
              id="qerds-body"
              className={`${control(false)} min-h-40 py-2 leading-relaxed`}
              value={body}
              onChange={(event) => setBody(event.target.value)}
            />
          </Field>

          <Field
            id="qerds-attachments"
            label={t("qerds.compose.attachmentsLabel")}
          >
            <input
              ref={fileInputRef}
              id="qerds-attachments"
              type="file"
              multiple
              className="sr-only"
              onChange={(event) => {
                addFiles(event.target.files);
                // Reset so re-selecting the same file fires onChange again.
                event.target.value = "";
              }}
            />
            <div className="flex flex-col gap-2">
              <div>
                <Button
                  variant="secondary"
                  size="sm"
                  icon="add"
                  onClick={() => fileInputRef.current?.click()}
                >
                  {t("qerds.compose.addAttachment")}
                </Button>
              </div>
              {files.length > 0 && (
                <ul className="flex flex-col gap-1.5">
                  {files.map((file, index) => (
                    <li
                      key={`${file.name}:${file.size}:${file.lastModified}`}
                      className="border-line bg-surface-2 flex items-center gap-2 rounded-md border px-2.5 py-1.5"
                    >
                      <Icon
                        name="lock"
                        size={14}
                        className="text-ink-soft shrink-0"
                      />
                      <span className="text-ink flex-1 truncate text-[13px]">
                        {file.name}
                      </span>
                      <span className="text-muted shrink-0 text-[11.5px]">
                        {formatBytes(file.size)}
                      </span>
                      <Button
                        variant="ghost"
                        size="sm"
                        icon="close"
                        iconOnly
                        onClick={() => removeFile(index)}
                        aria-label={t("qerds.compose.removeAttachment", {
                          name: file.name,
                        })}
                      />
                    </li>
                  ))}
                </ul>
              )}
              {fileError && (
                <span role="alert" className="text-error text-[12px]">
                  {fileError}
                </span>
              )}
            </div>
          </Field>

          {send.isError && (
            <p
              role="alert"
              className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
            >
              {errorMessage(send.error, t)}
            </p>
          )}
        </Card>

        <Card variant="highlight" className="h-fit p-4">
          <div className="flex items-start gap-2.5">
            <Icon name="info" size={16} className="text-link mt-0.5 shrink-0" />
            <p className="text-ink text-[13px]">{t("qerds.compose.note")}</p>
          </div>
        </Card>
      </form>
    </>
  );
}
