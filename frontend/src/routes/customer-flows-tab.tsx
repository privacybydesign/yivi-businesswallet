import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateRequestMutation,
  useProofingCustomerFlowsQuery,
  useSetCustomerFlowsMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingChannel,
  ProofingCustomerFlow,
  ProofingFlow,
} from "../api/identity-proofing";
import {
  assignedFlows,
  assuranceLevelLabel,
  editedFlowSelection,
  isProofingStep,
  isRequestedAttribute,
  noProofingSessions,
  proofingErrorMessage,
  sendFormDecision,
} from "../lib/identity-proofing";
import { Button, Card, Input, Modal, Tag } from "../ui";
import { FlowEditor } from "./flow-editor";
import type { EditorMode } from "./flow-editor";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

// The flows assigned to the customer, as cards: what each asks for and how
// sure it is. An admin switches to the assignment editor, which lists every
// flow of the org; a member's list holds only the assigned ones.
export function FlowsTab({
  slug,
  customer,
  isAdmin,
  sessionsByFlow,
}: {
  slug: string;
  customer: ProofingCustomer;
  isAdmin: boolean;
  sessionsByFlow: Map<string, { sessions: number }>;
}): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingCustomerFlowsQuery(slug, customer.id);
  const assign = useSetCustomerFlowsMutation(slug, customer.id);
  const [view, setView] = useState<FlowsView>({ kind: "list" });

  if (flows.isPending) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  if (flows.isError) {
    return <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>;
  }
  const assigned = flows.data.filter((f) => f.assigned);

  // A flow created here is the customer's at once; the default only moves to
  // it when the customer had none.
  function assignNew(flow: ProofingFlow): void {
    assign.mutate({
      flowIds: [...customer.flowIds, flow.id],
      defaultFlowId: customer.defaultFlowId || flow.id,
    });
  }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <p className={`${HINT} max-w-2xl`}>{t("customers.flows.intro")}</p>
        {isAdmin && view.kind === "list" && (
          <div className="flex shrink-0 items-center gap-2">
            <Button variant="ghost" onClick={() => setView({ kind: "assign" })}>
              {t("customers.flows.assignExisting")}
            </Button>
            <Button
              variant="secondary"
              icon="add"
              onClick={() => setView({ kind: "editor", mode: { kind: "new" } })}
            >
              {t("customers.flows.newFlow")}
            </Button>
          </div>
        )}
        {isAdmin && view.kind === "assign" && (
          <Button
            variant="secondary"
            className="shrink-0"
            onClick={() => setView({ kind: "list" })}
          >
            {t("customers.flows.done")}
          </Button>
        )}
      </div>
      {assign.isError && (
        <p className={ERROR}>
          {t("customers.flows.assignFailed", {
            reason: proofingErrorMessage(assign.error, t),
          })}
        </p>
      )}
      {isAdmin && view.kind === "editor" && (
        <FlowEditor
          key={
            view.mode.kind === "new"
              ? "new"
              : `${view.mode.flow.id}@${view.mode.flow.version}`
          }
          slug={slug}
          mode={view.mode}
          onSaved={view.mode.kind === "new" ? assignNew : undefined}
          note={
            view.mode.kind === "edit"
              ? t("customers.flows.sharedEditNote")
              : t("customers.flows.newFlowNote", { name: customer.name })
          }
          onDone={() => setView({ kind: "list" })}
        />
      )}
      {isAdmin && view.kind === "assign" ? (
        <AssignedFlowsCard
          // Reseeded whenever the saved assignment changes.
          key={`${customer.flowIds.join(",")}@${customer.defaultFlowId ?? ""}`}
          slug={slug}
          customerId={customer.id}
          flows={flows.data}
        />
      ) : assigned.length === 0 ? (
        view.kind === "list" && (
          <Card className="text-ink-soft p-6 text-[13px]">
            {isAdmin
              ? t("customers.flows.noneAssignedAdmin")
              : t("customers.flows.noneAssigned")}
          </Card>
        )
      ) : (
        assigned.map((flow) => (
          <FlowCard
            key={flow.id}
            flow={flow}
            paused={customer.status === "paused"}
            noApiKey={!customer.hasApiKey}
            sessions={
              (sessionsByFlow.get(flow.id) ?? noProofingSessions()).sessions
            }
            onEdit={
              isAdmin
                ? () =>
                    setView({ kind: "editor", mode: { kind: "edit", flow } })
                : undefined
            }
          />
        ))
      )}
    </>
  );
}

// What the Flows tab shows above the cards: nothing, the flow editor (a new
// flow or a new version of one), or the assignment of existing flows.
type FlowsView =
  | { kind: "list" }
  | { kind: "editor"; mode: EditorMode }
  | { kind: "assign" };

function FlowCard({
  flow,
  paused,
  noApiKey,
  sessions,
  onEdit,
}: {
  flow: ProofingCustomerFlow;
  paused: boolean;
  noApiKey: boolean;
  sessions: number;
  onEdit?: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const data = (flow.requestedAttributes ?? [])
    .filter(isRequestedAttribute)
    .map((value) => t(`identityProofingFlows.attributes.${value}`));
  const steps = flow.steps
    .filter(isProofingStep)
    .map((step) => t(`identityProofingFlows.steps.${step}`));

  return (
    <Card className="grid gap-4 p-5 md:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-ink text-[14.5px] font-bold">{flow.name}</span>
          {paused ? (
            <Tag dot>{t("customers.status.paused")}</Tag>
          ) : noApiKey ? (
            <Tag tone="amber" dot>
              {t("customers.status.setupNeeded")}
            </Tag>
          ) : (
            <Tag tone="green" dot>
              {t("customers.flows.live")}
            </Tag>
          )}
          {flow.default && <Tag>{t("customers.flows.default")}</Tag>}
        </div>
        <div className="text-muted mt-1 truncate font-mono text-[11px]">
          {flow.id} ·{" "}
          {t("identityProofingFlows.versionShort", { version: flow.version })}
        </div>
        <div className="text-ink-soft mt-2 text-[12.5px]">
          {t("customers.flows.sessions", { count: sessions })}
        </div>
      </div>
      <div>
        <div className={CAPTION}>{t("customers.flows.requestedData")}</div>
        <div className="text-ink mt-1 text-[13px]">
          {data.length === 0 ? t("customers.flows.noData") : data.join(", ")}
        </div>
      </div>
      <div>
        <div className={CAPTION}>{t("customers.flows.assuranceSteps")}</div>
        <div className="text-ink mt-1 text-[13px] font-semibold">
          {flow.requiredAssuranceLevel
            ? t("customers.flows.eidas", {
                level: assuranceLevelLabel(flow.requiredAssuranceLevel, t),
              })
            : t("customers.flows.noAssurance")}
        </div>
        <div className="text-ink-soft text-[13px]">{steps.join(", ")}</div>
      </div>
      {onEdit && (
        <div className="md:text-right">
          <button
            type="button"
            onClick={onEdit}
            className="text-ink text-[12.5px] font-semibold hover:underline"
          >
            {t("customers.flows.edit")}
          </button>
        </div>
      )}
    </Card>
  );
}

// The send form in a dialog over the customer's flows a member may send on.
export function SendModal({
  slug,
  customer,
  isAdmin,
  onClose,
  onLink,
}: {
  slug: string;
  customer: ProofingCustomer;
  isAdmin: boolean;
  onClose: () => void;
  onLink: (url: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingCustomerFlowsQuery(slug, customer.id);

  return (
    <Modal
      title={t("customers.send.title")}
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
          customer={customer}
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

function AssignedFlowsCard({
  slug,
  customerId,
  flows,
}: {
  slug: string;
  customerId: string;
  flows: ProofingCustomerFlow[];
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSetCustomerFlowsMutation(slug, customerId);
  const [assigned, setAssigned] = useState<ReadonlySet<string>>(
    () => new Set(flows.filter((f) => f.assigned).map((f) => f.id)),
  );
  const [defaultId, setDefaultId] = useState(
    () => flows.find((f) => f.default)?.id ?? "",
  );
  // A default the admin unticked falls to the first flow still ticked.
  const { selection, dirty } = editedFlowSelection(flows, assigned, defaultId, {
    flowIds: flows.filter((f) => f.assigned).map((f) => f.id),
    defaultFlowId: flows.find((f) => f.default)?.id ?? "",
  });

  function toggle(id: string, on: boolean): void {
    setAssigned((current) => {
      const next = new Set(current);
      if (on) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    save.mutate(selection);
  }

  return (
    <Card className="p-6">
      <h2 className="font-display text-[16px] font-bold">
        {t("customers.flows.title")}
      </h2>
      <p className={`${HINT} mt-1`}>{t("customers.flows.hint")}</p>
      {flows.length === 0 ? (
        <p className="text-ink-soft mt-4 text-[13px]">
          {t("customers.flows.empty")}
        </p>
      ) : (
        <form className="mt-4 flex flex-col gap-4" onSubmit={submit} noValidate>
          <ul className="border-line divide-y rounded-lg border">
            {flows.map((flow) => {
              const checkboxId = `proofing-customer-flow-${flow.id}`;
              const on = assigned.has(flow.id);
              return (
                <li
                  key={flow.id}
                  className="flex flex-wrap items-start gap-x-6 gap-y-2 px-4 py-3"
                >
                  <div className="flex min-w-0 flex-1 items-start gap-2.5">
                    <input
                      id={checkboxId}
                      type="checkbox"
                      className="mt-0.5 h-4 w-4"
                      checked={on}
                      disabled={!flow.completable}
                      onChange={(event) =>
                        toggle(flow.id, event.target.checked)
                      }
                    />
                    <label
                      htmlFor={checkboxId}
                      className="flex flex-wrap items-center gap-2"
                    >
                      <span className="font-semibold">{flow.name}</span>
                      <Tag>
                        {t("identityProofingFlows.versionShort", {
                          version: flow.version,
                        })}
                      </Tag>
                      {flow.requiredAssuranceLevel && (
                        <Tag tone="blue">
                          {assuranceLevelLabel(flow.requiredAssuranceLevel, t)}
                        </Tag>
                      )}
                      {!flow.completable && (
                        <Tag tone="amber">
                          {t("identityProofingFlows.notCompletable")}
                        </Tag>
                      )}
                      {flow.needsReferencePhoto && (
                        <Tag>{t("identityProofingFlows.referencePhoto")}</Tag>
                      )}
                    </label>
                  </div>
                  <label className="flex items-center gap-2 text-[13px]">
                    <input
                      type="radio"
                      name="proofing-customer-default-flow"
                      className="h-4 w-4"
                      checked={on && selection.defaultFlowId === flow.id}
                      disabled={!on}
                      onChange={() => setDefaultId(flow.id)}
                    />
                    {t("customers.flows.default")}
                  </label>
                </li>
              );
            })}
          </ul>
          {save.isError && (
            <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
          )}
          <div>
            <Button type="submit" loading={save.isPending} disabled={!dirty}>
              {t("customers.flows.save")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}

// How a request reaches its subject: a mail that is the session, this
// screen, which walks the person present through it, or a link to the
// customer's hosted page that the member hands the person.
const SEND_CHANNELS: {
  value: ProofingChannel;
  key: "email" | "onScreen" | "link";
}[] = [
  { value: "email", key: "email" },
  { value: "on_screen", key: "onScreen" },
  { value: "hosted", key: "link" },
];

function SendForm({
  slug,
  customer,
  flows,
  isAdmin,
  onSent,
  onLink,
  onCancel,
}: {
  slug: string;
  customer: ProofingCustomer;
  flows: ProofingCustomerFlow[];
  isAdmin: boolean;
  onSent: () => void;
  onLink: (url: string) => void;
  onCancel: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateRequestMutation(slug);
  // A flow matched against the customer's own photo is the customer API's
  // to send: this form has no photo to give it.
  const { sendable, initial } = assignedFlows(
    flows.filter((f) => f.needsReferencePhoto !== true),
  );
  const photoOnly = flows.some((f) => f.assigned && f.needsReferencePhoto);
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
        `verify?${new URLSearchParams({ flow: flowId }).toString()}`,
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
        customerId: customer.id,
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
        {onScreen
          ? t("customers.send.hintOnScreen")
          : delivery === "hosted"
            ? t("customers.send.hintLink")
            : t("customers.send.hint")}
      </p>
      {sendable.length === 0 ? (
        <p className="text-ink-soft text-[13px]">
          {photoOnly
            ? t("customers.send.referencePhotoFlows")
            : isAdmin
              ? t("customers.send.noFlowsAdmin")
              : t("customers.send.noFlowsMember")}
        </p>
      ) : (
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          {photoOnly && (
            <p className={HINT}>{t("customers.send.referencePhotoFlows")}</p>
          )}
          <fieldset className="flex flex-col gap-2">
            <legend className={`${LABEL} mb-1`}>
              {t("customers.send.channel")}
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
                      {t(`customers.send.channels.${option.key}.title`)}
                    </span>
                    <span className={HINT}>
                      {t(`customers.send.channels.${option.key}.hint`)}
                    </span>
                  </span>
                </label>
              );
            })}
            {!mailable && (
              <p className={HINT}>{t("customers.send.diplomasOnScreen")}</p>
            )}
          </fieldset>
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-email" className={LABEL}>
              {onScreen
                ? t("customers.send.emailOptional")
                : t("customers.send.email")}
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
              <p className={ERROR}>{t("customers.send.emailRequired")}</p>
            )}
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-name" className={LABEL}>
              {t("customers.send.name")}
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
              {t("customers.send.nameHint")}
            </p>
            {touched && nameMissing && (
              <p className={ERROR}>{t("customers.send.nameRequired")}</p>
            )}
          </div>
          {matchable && (
            <div className="flex flex-col gap-1">
              <label htmlFor="proofing-subject-birth-date" className={LABEL}>
                {t("customers.send.birthDate")}
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
                {t("customers.send.birthDateHint")}
              </p>
            </div>
          )}
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-flow" className={LABEL}>
              {t("customers.send.flow")}
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
              {t("customers.send.cancel")}
            </Button>
            {onScreen ? (
              <Button type="submit" icon="scan_qrcode">
                {t("customers.send.submitOnScreen")}
              </Button>
            ) : delivery === "hosted" ? (
              <Button type="submit" loading={create.isPending}>
                {t("customers.send.submitLink")}
              </Button>
            ) : (
              <Button type="submit" icon="email" loading={create.isPending}>
                {t("customers.send.submit")}
              </Button>
            )}
          </div>
        </form>
      )}
    </div>
  );
}
