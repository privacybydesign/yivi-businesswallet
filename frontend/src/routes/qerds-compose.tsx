import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useQerdsAddressesQuery,
  useSendQerdsMessageMutation,
} from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Button, Card, Icon, TopBar } from "../ui";
import * as React from "react";

const FORM_ID = "qerds-compose-form";
const CONFLICT_STATUS = 409;
// Plausible address check only; the provider/directory is the authority.
const ADDRESS_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

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

  const org = useOrganizationQuery(slug);
  const addresses = useQerdsAddressesQuery(slug, !org.isError);
  const send = useSendQerdsMessageMutation(slug);

  const [recipient, setRecipient] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [attempted, setAttempted] = useState(false);

  const defaultAddress = addresses.data?.find((address) => address.isDefault);
  const backToList = (): void => void navigate(`/${slug}/qerds`);

  const errors = attempted ? validate({ recipient, subject }) : {};

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
        recipient: recipient.trim(),
        subject: subject.trim(),
        body,
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
            <input
              id="qerds-from"
              className={`${control(false)} h-9`}
              value={defaultAddress?.address ?? ""}
              placeholder={t("qerds.compose.noAddressPlaceholder")}
              readOnly
              disabled
            />
          </Field>

          <Field
            id="qerds-recipient"
            label={t("qerds.compose.recipient")}
            required
            error={errors.recipient && t(errors.recipient)}
          >
            <input
              id="qerds-recipient"
              className={`${control(Boolean(errors.recipient))} h-9`}
              value={recipient}
              onChange={(event) => setRecipient(event.target.value)}
              placeholder={t("qerds.compose.recipientPlaceholder")}
              aria-required
              aria-invalid={errors.recipient ? true : undefined}
              aria-describedby={
                errors.recipient ? "qerds-recipient-error" : undefined
              }
              autoFocus
            />
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
