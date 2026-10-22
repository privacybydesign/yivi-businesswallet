import { useId, useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateRequestMutation,
  useDataRequestFlowsQuery,
  useProofingFlowsQuery,
  useProofingSettingsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingChannel, ProofingFlow } from "../api/identity-proofing";
import {
  SEND_KINDS,
  mailLifetimeMinutes,
  proofingErrorMessage,
  requestTarget,
  sendFormDecision,
  sendKindFlows,
  sendRequestFields,
  verifySearch,
} from "../lib/identity-proofing";
import type { SendKind } from "../lib/identity-proofing";
import { Button, Input, Modal } from "../ui";
import { ChevronSelect } from "./flow-editor-parts";
import { SecretReveal } from "./proofing-ui";

const LABEL = "text-ink text-[14px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const OPTION_HINT = "text-ink-soft text-[13px] leading-5";
const ERROR = "text-error text-[12.5px]";
const SEND_FORM = "proofing-send";
const EMAIL_ERROR_ID = "proofing-subject-email-error";
const NAME_ERROR_ID = "proofing-subject-name-error";
const BIRTH_DATE_ERROR_ID = "proofing-subject-birth-date-error";

// The "Verify a person" button and its dialog: any member sends an external
// person a request (the person needs no account), and an admin also a data
// request (See my data, Delete my data). A hosted link is shown to copy once
// the dialog has closed, so closing that dialog cannot discard it. flowId is
// the flow the button belongs to: the dialog then sends on it, and asks
// neither what to send nor the flow.
export function VerifyPerson({
  slug,
  isAdmin,
  flowId,
}: {
  slug: string;
  isAdmin: boolean;
  flowId?: string;
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
          startFlowId={flowId}
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
// for an admin, the ones members may use for a member, besides an admin's
// data requests.
function SendModal({
  slug,
  isAdmin,
  startFlowId,
  onClose,
  onLink,
}: {
  slug: string;
  isAdmin: boolean;
  startFlowId?: string;
  onClose: () => void;
  onLink: (url: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingFlowsQuery(slug);

  if (flows.isPending || flows.isError) {
    return (
      <Modal
        title={t("identityProofing.send.title")}
        closeLabel={t("common.close")}
        onClose={onClose}
      >
        {flows.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : (
          <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>
        )}
      </Modal>
    );
  }

  return (
    <SendForm
      slug={slug}
      flows={flows.data}
      isAdmin={isAdmin}
      startFlowId={startFlowId}
      onSent={onClose}
      onLink={onLink}
      onCancel={onClose}
    />
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

const SUBMIT_KEYS = {
  email: "submit",
  on_screen: "submitOnScreen",
  hosted: "submitLink",
} as const satisfies Record<ProofingChannel, string>;

function SendForm({
  slug,
  flows,
  isAdmin,
  startFlowId,
  onSent,
  onLink,
  onCancel,
}: {
  slug: string;
  flows: ProofingFlow[];
  isAdmin: boolean;
  startFlowId?: string;
  onSent: () => void;
  onLink: (url: string) => void;
  onCancel: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateRequestMutation(slug);
  const settings = useProofingSettingsQuery(slug);
  // Opened from the Overview or All people, an admin first picks what to
  // send: an identity check on a flow, or one of the data requests every org
  // offers, which runs on the org's own flow of its kind. Opened from a flow,
  // the dialog sends that flow.
  const choosesKind = isAdmin && startFlowId === undefined;
  const [kind, setKind] = useState<SendKind>("identity");
  const dataRequest = kind !== "identity";
  const dataFlows = useDataRequestFlowsQuery(slug, choosesKind && dataRequest);
  // A flow matched against the sender's own photo is the API's to send: this
  // form has no photo to give it.
  const { sendable, initial } = sendKindFlows(kind, flows, dataFlows.data);
  const start = sendable.find((f) => f.id === startFlowId) ?? initial;
  const photoOnly = !dataRequest && flows.some((f) => f.needsReferencePhoto);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [birthDate, setBirthDate] = useState("");
  const [nameOn, setNameOn] = useState(false);
  const [passOn, setPassOn] = useState(false);
  const [picked, setPicked] = useState("");
  const [touched, setTouched] = useState(false);
  const [channel, setChannel] = useState<ProofingChannel>("email");
  const navigate = useNavigate();

  if (!choosesKind && sendable.length === 0) {
    return (
      <Modal
        title={t("identityProofing.send.title")}
        closeLabel={t("common.close")}
        onClose={onCancel}
      >
        <p className="text-ink-soft text-[13px]">
          {photoOnly
            ? t("identityProofing.send.referencePhotoFlows")
            : isAdmin
              ? t("identityProofing.send.noFlowsAdmin")
              : t("identityProofing.send.noFlowsMember")}
        </p>
      </Modal>
    );
  }

  // The flow the dialog was opened from is not asked for again, unless it
  // cannot be sent from here: the list then shows the flow it falls back to.
  const askFlow =
    !dataRequest &&
    sendable.length > 0 &&
    (startFlowId === undefined || !sendable.some((f) => f.id === startFlowId));
  const { flowId, linkable, mailable, matchable, delivery } = sendFormDecision({
    sendable,
    initial: start,
    picked: dataRequest ? "" : picked,
    channel,
  });
  const picks = sendable.find((f) => f.id === flowId);
  const mailMinutes = mailLifetimeMinutes(
    picks,
    settings.data?.sessionTtlSeconds,
  );
  const fields = sendRequestFields({
    delivery,
    matchable,
    nameOn,
    passOn,
    email,
    name,
    birthDate,
  });
  const showEmailError = touched && fields.missing.email;
  const showNameError = touched && fields.missing.name;
  const showBirthDateError = touched && fields.missing.birthDate;
  // A data request's flow is read from the backend once it is picked.
  const flowPending = dataRequest && dataFlows.isPending;
  const flowError = dataRequest && dataFlows.isError ? dataFlows.error : null;
  const channels = SEND_CHANNELS.filter(
    (option) => linkable || option.value === "on_screen",
  );

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (Object.values(fields.missing).includes(true) || flowId === "") {
      return;
    }
    if (delivery === "on_screen") {
      // The session starts on the page, once the person has picked an app.
      void navigate(
        `/${slug}/identity-proofing/verify?${verifySearch(kind, flowId)}`,
        {
          state: {
            name: fields.name,
            email: fields.email,
            birthDate: fields.birthDate,
          },
        },
      );
      return;
    }
    create.mutate(
      {
        email: fields.email,
        name: fields.name,
        birthDate: fields.birthDate,
        channel: delivery,
        ...requestTarget(kind, flowId),
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
    <Modal
      title={t("identityProofing.send.title")}
      closeLabel={t("common.close")}
      onClose={onCancel}
      footer={
        <>
          <Button variant="ghost" onClick={onCancel}>
            {t("identityProofing.send.cancel")}
          </Button>
          <Button
            type="submit"
            form={SEND_FORM}
            loading={create.isPending}
            disabled={flowId === ""}
          >
            {t(`identityProofing.send.${SUBMIT_KEYS[delivery]}`)}
          </Button>
        </>
      }
    >
      <form
        id={SEND_FORM}
        className="flex flex-col gap-5"
        onSubmit={submit}
        noValidate
      >
        {choosesKind && (
          <fieldset className="flex flex-col gap-2.5">
            <legend className={`${LABEL} mb-1`}>
              {t("identityProofing.send.what.legend")}
            </legend>
            {SEND_KINDS.map((option) => (
              <OptionCard
                key={option}
                name="proofing-send-kind"
                value={option}
                title={t(`identityProofing.send.what.${option}.title`)}
                hint={t(`identityProofing.send.what.${option}.hint`)}
                checked={kind === option}
                onSelect={() => setKind(option)}
              />
            ))}
          </fieldset>
        )}
        {photoOnly && (
          <p className={HINT}>
            {t("identityProofing.send.referencePhotoFlows")}
          </p>
        )}
        {!dataRequest && sendable.length === 0 && (
          <p className="text-ink-soft text-[13px]">
            {t("identityProofing.send.noFlowsAdmin")}
          </p>
        )}
        {askFlow && (
          <div className="flex flex-col gap-1.5">
            <label htmlFor="proofing-subject-flow" className={LABEL}>
              {t("identityProofing.send.flow")}
            </label>
            <ChevronSelect
              id="proofing-subject-flow"
              value={flowId}
              onChange={setPicked}
            >
              {sendable.map((flow) => (
                <option key={flow.id} value={flow.id}>
                  {flow.name}
                </option>
              ))}
            </ChevronSelect>
          </div>
        )}
        {flowPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : flowError ? (
          <p className={ERROR}>{proofingErrorMessage(flowError, t)}</p>
        ) : (
          flowId !== "" && (
            <>
              <fieldset className="flex flex-col gap-2.5">
                <legend className={`${LABEL} mb-1`}>
                  {t("identityProofing.send.channel")}
                </legend>
                {channels.map((option) => (
                  <OptionCard
                    key={option.value}
                    name="proofing-channel"
                    value={option.value}
                    title={t(
                      `identityProofing.send.channels.${option.key}.title`,
                    )}
                    hint={[
                      t(`identityProofing.send.channels.${option.key}.hint`),
                      option.value === "email" && mailMinutes !== undefined
                        ? t("identityProofing.send.mailLifetime", {
                            minutes: mailMinutes,
                          })
                        : "",
                    ]
                      .filter((part) => part !== "")
                      .join(" ")}
                    checked={delivery === option.value}
                    disabled={option.value === "email" && !mailable}
                    onSelect={() => setChannel(option.value)}
                    footer={
                      option.value === "email" &&
                      delivery === "email" && (
                        <>
                          <label
                            htmlFor="proofing-subject-email"
                            className={LABEL}
                          >
                            {t("identityProofing.send.email")}
                          </label>
                          <Input
                            id="proofing-subject-email"
                            type="email"
                            autoComplete="off"
                            placeholder={t(
                              "identityProofing.send.emailPlaceholder",
                            )}
                            value={email}
                            aria-invalid={showEmailError}
                            aria-describedby={
                              showEmailError ? EMAIL_ERROR_ID : undefined
                            }
                            onChange={(event) => setEmail(event.target.value)}
                          />
                          {showEmailError && (
                            <p id={EMAIL_ERROR_ID} className={ERROR}>
                              {t("identityProofing.send.emailRequired")}
                            </p>
                          )}
                        </>
                      )
                    }
                  />
                ))}
                {!linkable ? (
                  <p className={HINT}>
                    {t("identityProofing.send.notLinkable")}
                  </p>
                ) : (
                  !mailable && (
                    <p className={HINT}>
                      {t("identityProofing.send.diplomasOnScreen")}
                    </p>
                  )
                )}
              </fieldset>
              <div className="border-line flex flex-col gap-4 border-t pt-4.5">
                <div className="flex flex-col gap-3">
                  <SwitchRow
                    checked={fields.nameShown}
                    disabled={fields.passing}
                    title={t("identityProofing.send.addName.title")}
                    hint={t("identityProofing.send.addName.hint")}
                    note={
                      fields.passing
                        ? t("identityProofing.send.addName.passNote")
                        : undefined
                    }
                    onChange={setNameOn}
                  />
                  {fields.nameShown && (
                    <div className="flex flex-col gap-1.5">
                      <label htmlFor="proofing-subject-name" className={LABEL}>
                        {t("identityProofing.send.name")}{" "}
                        <span className="text-ink-soft font-normal">
                          {t("identityProofing.send.required")}
                        </span>
                      </label>
                      <Input
                        id="proofing-subject-name"
                        autoComplete="off"
                        value={name}
                        aria-invalid={showNameError}
                        aria-describedby={
                          showNameError ? NAME_ERROR_ID : undefined
                        }
                        onChange={(event) => setName(event.target.value)}
                      />
                      {showNameError && (
                        <p id={NAME_ERROR_ID} className={ERROR}>
                          {t("identityProofing.send.nameRequired")}
                        </p>
                      )}
                    </div>
                  )}
                </div>
                {matchable && (
                  <div className="flex flex-col gap-3">
                    <SwitchRow
                      checked={fields.passing}
                      title={t("identityProofing.send.onlyThisPerson.title")}
                      hint={t("identityProofing.send.onlyThisPerson.hint")}
                      onChange={setPassOn}
                    />
                    {fields.passing && (
                      <div className="flex w-50 flex-col gap-1.5">
                        <label
                          htmlFor="proofing-subject-birth-date"
                          className={LABEL}
                        >
                          {t("identityProofing.send.birthDate")}{" "}
                          <span className="text-ink-soft font-normal">
                            {t("identityProofing.send.required")}
                          </span>
                        </label>
                        <Input
                          id="proofing-subject-birth-date"
                          type="date"
                          autoComplete="off"
                          value={birthDate}
                          aria-invalid={showBirthDateError}
                          aria-describedby={
                            showBirthDateError ? BIRTH_DATE_ERROR_ID : undefined
                          }
                          onChange={(event) => setBirthDate(event.target.value)}
                        />
                        {showBirthDateError && (
                          <p id={BIRTH_DATE_ERROR_ID} className={ERROR}>
                            {t("identityProofing.send.birthDateRequired")}
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </>
          )
        )}
        {create.isError && (
          <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
        )}
      </form>
    </Modal>
  );
}

// A radio card: one thing to send, or one way to get the QR code. The footer
// sits outside the label so its field is not part of the radio's name.
function OptionCard({
  name,
  value,
  title,
  hint,
  checked,
  disabled = false,
  onSelect,
  footer,
}: {
  name: string;
  value: string;
  title: string;
  hint: string;
  checked: boolean;
  disabled?: boolean;
  onSelect: () => void;
  footer?: React.ReactNode;
}): React.JSX.Element {
  const titleId = useId();
  const hintId = useId();

  return (
    <div
      className={[
        "rounded-yivi has-[input[type=radio]:focus-visible]:ring-ink/10 flex flex-col gap-3.5 border px-4 py-3.5 has-[input[type=radio]:focus-visible]:ring-3",
        checked ? "border-ink bg-surface-2" : "border-line-strong bg-surface",
        disabled ? "opacity-60" : "",
      ].join(" ")}
    >
      <label
        className={`flex items-start gap-3 ${disabled ? "cursor-not-allowed" : "cursor-pointer"}`}
      >
        <input
          type="radio"
          name={name}
          value={value}
          checked={checked}
          disabled={disabled}
          aria-labelledby={titleId}
          aria-describedby={hintId}
          onChange={onSelect}
          className="accent-ink mt-0.5 h-[18px] w-[18px] flex-none"
        />
        <span className="flex flex-col gap-0.5">
          <span id={titleId} className={LABEL}>
            {title}
          </span>
          <span id={hintId} className={OPTION_HINT}>
            {hint}
          </span>
        </span>
      </label>
      {footer && <div className="flex flex-col gap-1.5 pl-7.5">{footer}</div>}
    </div>
  );
}

// The note says why the switch cannot be changed.
function SwitchRow({
  checked,
  disabled = false,
  title,
  hint,
  note,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  title: string;
  hint: string;
  note?: string;
  onChange: (checked: boolean) => void;
}): React.JSX.Element {
  const titleId = useId();
  const hintId = useId();
  const noteId = useId();

  return (
    <label
      className={`flex items-start justify-between gap-4 ${disabled ? "cursor-default" : "cursor-pointer"}`}
    >
      <span className="flex flex-col gap-0.5">
        <span id={titleId} className={LABEL}>
          {title}
        </span>
        <span id={hintId} className={OPTION_HINT}>
          {hint}
        </span>
        {note && (
          <span id={noteId} className={HINT}>
            {note}
          </span>
        )}
      </span>
      <span className="relative mt-0.5 h-6 w-11 flex-none">
        <input
          type="checkbox"
          role="switch"
          checked={checked}
          disabled={disabled}
          aria-labelledby={titleId}
          aria-describedby={note ? `${hintId} ${noteId}` : hintId}
          onChange={(event) => onChange(event.target.checked)}
          className="peer absolute inset-0 m-0 h-6 w-11 cursor-[inherit] opacity-0"
        />
        <span
          aria-hidden="true"
          className="bg-line-strong peer-checked:bg-ink peer-focus-visible:outline-ink absolute inset-0 rounded-full transition-colors peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-disabled:opacity-60 motion-reduce:transition-none"
        />
        <span
          aria-hidden="true"
          className="bg-surface shadow-card absolute top-0.5 left-0.5 h-5 w-5 rounded-full transition-transform peer-checked:translate-x-5 motion-reduce:transition-none"
        />
      </span>
    </label>
  );
}
