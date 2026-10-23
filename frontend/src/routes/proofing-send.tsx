import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateRequestMutation,
  useProofingFlowsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingChannel, ProofingFlow } from "../api/identity-proofing";
import {
  externalFlows,
  proofingErrorMessage,
  sendFormDecision,
} from "../lib/identity-proofing";
import { Button, Input, Modal } from "../ui";
import { SecretReveal } from "./proofing-ui";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

// The "Verify a person" button and its dialog: any member sends an external
// person a request (an e-mail address and an optional name; the person needs no
// account). A hosted link is shown to copy once the dialog has closed, so
// closing that dialog cannot discard it.
export function VerifyPerson({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [sending, setSending] = useState(false);
  const [link, setLink] = useState<string>();

  return (
    <>
      <Button icon="email" onClick={() => setSending(true)}>
        {t("identityProofing.send.title")}
      </Button>
      {sending && (
        <SendModal
          slug={slug}
          isAdmin={isAdmin}
          onClose={() => setSending(false)}
          onLink={(url) => {
            setSending(false);
            setLink(url);
          }}
        />
      )}
      {link !== undefined && (
        <SecretReveal
          title={t("identityProofing.send.linkTitle")}
          hint={t("identityProofing.send.linkHint")}
          secret={link}
          doneLabel={t("identityProofing.send.linkDone")}
          onClose={() => setLink(undefined)}
        />
      )}
    </>
  );
}

// The send form in a dialog over the flows the sender may send on: every flow
// for an admin, the ones members may use for a member.
function SendModal({
  slug,
  isAdmin,
  onClose,
  onLink,
}: {
  slug: string;
  isAdmin: boolean;
  onClose: () => void;
  onLink: (url: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingFlowsQuery(slug);

  return (
    <Modal
      title={t("identityProofing.send.title")}
      closeLabel={t("common.close")}
      onClose={onClose}
    >
      {flows.isPending ? (
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      ) : flows.isError ? (
        <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>
      ) : (
        <SendForm
          slug={slug}
          flows={flows.data}
          isAdmin={isAdmin}
          onSent={onClose}
          onLink={onLink}
          onCancel={onClose}
        />
      )}
    </Modal>
  );
}

// How a request reaches its subject: a mail that is the session, this
// screen, which walks the person present through it, or a link to the
// hosted page that the member hands the person.
const SEND_CHANNELS: {
  value: ProofingChannel;
  key: "email" | "onScreen" | "link";
}[] = [
  { value: "email", key: "email" },
  { value: "on_screen", key: "onScreen" },
  { value: "hosted", key: "link" },
];

// The line above the form: what the delivery does.
const HINT_KEYS = {
  email: "hint",
  on_screen: "hintOnScreen",
  hosted: "hintLink",
} as const satisfies Record<ProofingChannel, string>;

function SendForm({
  slug,
  flows,
  isAdmin,
  onSent,
  onLink,
  onCancel,
}: {
  slug: string;
  flows: ProofingFlow[];
  isAdmin: boolean;
  onSent: () => void;
  onLink: (url: string) => void;
  onCancel: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateRequestMutation(slug);
  // A flow matched against the sender's own photo is the API's to send: this
  // form has no photo to give it.
  const { sendable, initial } = externalFlows(flows);
  const photoOnly = flows.some((f) => f.needsReferencePhoto);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [birthDate, setBirthDate] = useState("");
  const [picked, setPicked] = useState("");
  const [touched, setTouched] = useState(false);
  const [channel, setChannel] = useState<ProofingChannel>("email");
  const navigate = useNavigate();
  const {
    flowId,
    mailable,
    matchable,
    expectedBirthDate: expected,
    delivery,
    emailMissing,
    nameMissing,
  } = sendFormDecision({
    sendable,
    initial,
    picked,
    channel,
    email,
    name,
    birthDate,
  });
  const onScreen = delivery === "on_screen";

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (emailMissing || nameMissing || flowId === "") {
      return;
    }
    if (onScreen) {
      // The session starts on the page, once the person has picked an app.
      void navigate(
        `/${slug}/identity-proofing/verify?${new URLSearchParams({ flow: flowId }).toString()}`,
        {
          state: {
            name: name.trim(),
            email: email.trim(),
            birthDate: expected || undefined,
          },
        },
      );
      return;
    }
    create.mutate(
      {
        email: email.trim(),
        name: name.trim(),
        birthDate: expected || undefined,
        flowId,
        channel: delivery,
      },
      {
        // A link is shown to copy once the form closes.
        onSuccess: (sent) =>
          delivery === "hosted" && sent.hostedUrl
            ? onLink(sent.hostedUrl)
            : onSent(),
      },
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <p className={HINT}>
        {t(`identityProofing.send.${HINT_KEYS[delivery]}`)}
      </p>
      {sendable.length === 0 ? (
        <p className="text-ink-soft text-[13px]">
          {photoOnly
            ? t("identityProofing.send.referencePhotoFlows")
            : isAdmin
              ? t("identityProofing.send.noFlowsAdmin")
              : t("identityProofing.send.noFlowsMember")}
        </p>
      ) : (
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          {photoOnly && (
            <p className={HINT}>
              {t("identityProofing.send.referencePhotoFlows")}
            </p>
          )}
          <fieldset className="flex flex-col gap-2">
            <legend className={`${LABEL} mb-1`}>
              {t("identityProofing.send.channel")}
            </legend>
            {SEND_CHANNELS.map((option) => {
              const checked = delivery === option.value;
              const disabled = option.value === "email" && !mailable;
              return (
                <label
                  key={option.value}
                  className={[
                    "rounded-yivi flex items-start gap-3 border p-3",
                    disabled
                      ? "cursor-not-allowed opacity-60"
                      : "cursor-pointer",
                    checked
                      ? "border-primary bg-highlight"
                      : "border-line-strong bg-surface",
                  ].join(" ")}
                >
                  <input
                    type="radio"
                    name="proofing-channel"
                    value={option.value}
                    checked={checked}
                    disabled={disabled}
                    onChange={() => setChannel(option.value)}
                    className="mt-1"
                  />
                  <span className="flex flex-col gap-0.5">
                    <span className="text-ink text-[13.5px] font-semibold">
                      {t(`identityProofing.send.channels.${option.key}.title`)}
                    </span>
                    <span className={HINT}>
                      {t(`identityProofing.send.channels.${option.key}.hint`)}
                    </span>
                  </span>
                </label>
              );
            })}
            {!mailable && (
              <p className={HINT}>
                {t("identityProofing.send.diplomasOnScreen")}
              </p>
            )}
          </fieldset>
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-email" className={LABEL}>
              {onScreen
                ? t("identityProofing.send.emailOptional")
                : t("identityProofing.send.email")}
            </label>
            <Input
              id="proofing-subject-email"
              type="email"
              autoComplete="off"
              value={email}
              aria-invalid={touched && emailMissing}
              onChange={(event) => setEmail(event.target.value)}
            />
            {touched && emailMissing && (
              <p className={ERROR}>
                {t("identityProofing.send.emailRequired")}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-name" className={LABEL}>
              {t("identityProofing.send.name")}
            </label>
            <Input
              id="proofing-subject-name"
              autoComplete="off"
              value={name}
              aria-invalid={touched && nameMissing}
              aria-describedby="proofing-subject-name-hint"
              onChange={(event) => setName(event.target.value)}
            />
            <p id="proofing-subject-name-hint" className={HINT}>
              {t("identityProofing.send.nameHint")}
            </p>
            {touched && nameMissing && (
              <p className={ERROR}>{t("identityProofing.send.nameRequired")}</p>
            )}
          </div>
          {matchable && (
            <div className="flex flex-col gap-1">
              <label htmlFor="proofing-subject-birth-date" className={LABEL}>
                {t("identityProofing.send.birthDate")}
              </label>
              <Input
                id="proofing-subject-birth-date"
                type="date"
                autoComplete="off"
                value={birthDate}
                aria-describedby="proofing-subject-birth-date-hint"
                onChange={(event) => setBirthDate(event.target.value)}
              />
              <p id="proofing-subject-birth-date-hint" className={HINT}>
                {t("identityProofing.send.birthDateHint")}
              </p>
            </div>
          )}
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-flow" className={LABEL}>
              {t("identityProofing.send.flow")}
            </label>
            <select
              id="proofing-subject-flow"
              className={SELECT_CLASS}
              value={flowId}
              onChange={(event) => setPicked(event.target.value)}
            >
              {sendable.map((flow) => (
                <option key={flow.id} value={flow.id}>
                  {flow.name}
                </option>
              ))}
            </select>
          </div>
          {create.isError && (
            <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="secondary" onClick={onCancel}>
              {t("identityProofing.send.cancel")}
            </Button>
            {onScreen ? (
              <Button type="submit" icon="scan_qrcode">
                {t("identityProofing.send.submitOnScreen")}
              </Button>
            ) : delivery === "hosted" ? (
              <Button type="submit" loading={create.isPending}>
                {t("identityProofing.send.submitLink")}
              </Button>
            ) : (
              <Button type="submit" icon="email" loading={create.isPending}>
                {t("identityProofing.send.submit")}
              </Button>
            )}
          </div>
        </form>
      )}
    </div>
  );
}
