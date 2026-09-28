import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useCreateProofingRequestMutation,
  useProofingCustomerFlowsQuery,
  useProofingCustomerQuery,
  useProofingRequestsQuery,
  useProofingRequestEventsQuery,
  useProofingStatsQuery,
  useSetProofingCustomerFlowsMutation,
  useUpdateProofingCustomerMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
  ProofingFlow,
  ProofingRequest,
} from "../api/identity-proofing";
import type { TFunction } from "i18next";
import type { AuditEvent } from "../api/organization";
import {
  AUDIT_TONE_CLASSES,
  auditActionLabel,
  auditVisual,
} from "../lib/audit-event";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import {
  assignedFlows,
  editedFlowSelection,
  isProofingStep,
  isRequestedAttribute,
  noProofingSessions,
  proofingErrorMessage,
  proofingStatsBy,
  isProofingLive,
  proofingMethodLabel,
  proofingRejectionReason,
  requestSubject,
  sessionEventDetail,
  SESSION_FILTERS,
  formatDuration,
  matchesSessionFilter,
  sessionDurationSeconds,
  sessionFilterCounts,
  shortRequestId,
  sumProofingStats,
} from "../lib/identity-proofing";
import type { SessionFilter } from "../lib/identity-proofing";
import {
  Button,
  Card,
  ConfirmDialog,
  Icon,
  Input,
  Modal,
  Table,
  Tag,
  TopBar,
} from "../ui";
import { ApiKeysTab } from "./customer-api-keys-tab";
import { FlowEditor } from "./identity-proofing-flows";
import type { EditorMode } from "./identity-proofing-flows";
import { BrandingTab } from "./customer-branding-tab";
import { SettingsTab } from "./customer-settings-tab";
import { WebhooksTab } from "./customer-webhooks-tab";
import {
  CustomerMark,
  CustomerStatusTag,
  ResultTag,
} from "./proofing-customer-ui";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
const HTTP_NOT_FOUND = 404;
const SESSION_COLUMNS = 8;
const OPEN_ICON_SIZE = 18;
const TIMELINE_ICON_SIZE = 14;
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

type TabKey =
  | "flows"
  | "branding"
  | "apiKeys"
  | "webhooks"
  | "sessions"
  | "settings";
const DEFAULT_TAB: TabKey = "flows";

type Tab = { key: TabKey; labelKey: `customers.tabs.${TabKey}` };
const tab = (key: TabKey): Tab => ({ key, labelKey: `customers.tabs.${key}` });
// A member sends for the customer and follows its sessions; the rest is the
// admin's configuration of it.
const MEMBER_TABS: Tab[] = [tab("flows"), tab("sessions")];
const ADMIN_TABS: Tab[] = [
  tab("flows"),
  tab("branding"),
  tab("apiKeys"),
  tab("webhooks"),
  tab("sessions"),
  tab("settings"),
];

// One customer of the org, in tabs: the flows assigned to it (an admin assigns
// them here), the sessions sent for it, and, for an admin, its settings. Any
// member verifies a person for it from the header (an e-mail address and an
// optional name; the person needs no account) unless an admin paused it. An
// admin sees every session for the customer, a member the ones they sent.
export default function CustomerDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, customerId } = useParams();
  // Guaranteed by the ":orgSlug/…/:customerId" route this component mounts under.
  const slug = orgSlug!;
  const id = customerId!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const customer = useProofingCustomerQuery(slug, id);
  const stats = useProofingStatsQuery(slug);
  const formatDate = useDateFormatter();
  const [searchParams, setSearchParams] = useSearchParams();
  const [sending, setSending] = useState(false);
  const [confirmingPause, setConfirmingPause] = useState(false);
  const update = useUpdateProofingCustomerMutation(slug, id);

  const tabs = isAdmin ? ADMIN_TABS : MEMBER_TABS;
  const requested = searchParams.get("tab");
  // A member landing on ?tab=settings (a shared link) gets the default tab.
  const activeTab: TabKey =
    tabs.find((tab) => tab.key === requested)?.key ?? DEFAULT_TAB;
  const setTab = (tab: TabKey): void =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (tab === DEFAULT_TAB) next.delete("tab");
        else next.set("tab", tab);
        return next;
      },
      { replace: true },
    );

  const rows = (stats.data?.rows ?? []).filter((r) => r.customerId === id);
  const sessions = sumProofingStats(rows).sessions;
  const notFound =
    customer.error instanceof ApiError &&
    customer.error.status === HTTP_NOT_FOUND;
  const paused = customer.data?.status === "paused";

  return (
    <>
      <TopBar
        title={customer.data?.name ?? t("customers.title")}
        leading={
          customer.data && <CustomerMark customer={customer.data} size="lg" />
        }
        badges={customer.data && <CustomerStatusTag customer={customer.data} />}
        subtitle={
          customer.data &&
          t("customers.detail.since", {
            id: shortRequestId(customer.data.id),
            date: formatDate(customer.data.createdAt),
            count: sessions,
          })
        }
        actions={
          customer.data && (
            <>
              {isAdmin && (
                <Button
                  variant="secondary"
                  loading={update.isPending}
                  onClick={() =>
                    paused
                      ? update.mutate({ paused: false })
                      : setConfirmingPause(true)
                  }
                >
                  {paused
                    ? t("customers.detail.resume")
                    : t("customers.detail.pause")}
                </Button>
              )}
              <Button
                icon="email"
                disabled={paused}
                onClick={() => setSending(true)}
              >
                {t("customers.detail.verify")}
              </Button>
            </>
          )
        }
      />
      {customer.data && sending && (
        <SendModal
          slug={slug}
          customer={customer.data}
          isAdmin={isAdmin}
          onClose={() => setSending(false)}
        />
      )}
      {customer.data && confirmingPause && (
        <ConfirmDialog
          title={t("customers.detail.pauseConfirm.title", {
            name: customer.data.name,
          })}
          message={t("customers.detail.pauseConfirm.message")}
          confirmLabel={t("customers.detail.pauseConfirm.confirm")}
          busy={update.isPending}
          onConfirm={() =>
            update.mutate(
              { paused: true },
              { onSuccess: () => setConfirmingPause(false) },
            )
          }
          onClose={() => setConfirmingPause(false)}
        />
      )}
      {customer.isPending ? (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">
          {t("common.loading")}
        </p>
      ) : customer.isError ? (
        <p className={`${ERROR} p-4 sm:p-8`}>
          {notFound
            ? t("customers.detail.notFound")
            : proofingErrorMessage(customer.error, t)}
        </p>
      ) : (
        <>
          <div
            role="tablist"
            aria-label={customer.data.name}
            className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8"
            onKeyDown={(e) => {
              if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
              e.preventDefault();
              const i = tabs.findIndex((tab) => tab.key === activeTab);
              const delta = e.key === "ArrowRight" ? 1 : -1;
              const nextTab = tabs[(i + delta + tabs.length) % tabs.length];
              setTab(nextTab.key);
              document.getElementById(`customer-tab-${nextTab.key}`)?.focus();
            }}
          >
            {tabs.map((tab) => {
              const active = activeTab === tab.key;
              return (
                <button
                  key={tab.key}
                  id={`customer-tab-${tab.key}`}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  aria-controls="customer-tabpanel"
                  tabIndex={active ? 0 : -1}
                  onClick={() => setTab(tab.key)}
                  className={[
                    "h-11 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
                    active
                      ? "border-primary text-ink font-semibold"
                      : "text-ink-soft hover:text-ink border-transparent font-medium",
                  ].join(" ")}
                >
                  {t(tab.labelKey)}
                </button>
              );
            })}
          </div>
          <div
            id="customer-tabpanel"
            role="tabpanel"
            aria-labelledby={`customer-tab-${activeTab}`}
            className="flex flex-col gap-4 p-4 sm:p-8"
          >
            {paused && (
              <p className="bg-warning-bg text-warning-fg rounded-yivi px-4 py-3 text-[13px]">
                {t("customers.detail.pausedNotice")}
              </p>
            )}
            {activeTab === "flows" && (
              <FlowsTab
                slug={slug}
                customer={customer.data}
                isAdmin={isAdmin}
                sessionsByFlow={proofingStatsBy(rows, (r) => r.flowId)}
              />
            )}
            {activeTab === "branding" && isAdmin && (
              <BrandingTab
                // Reseeded when the saved branding changes.
                key={customer.data.updatedAt}
                slug={slug}
                customer={customer.data}
              />
            )}
            {activeTab === "apiKeys" && isAdmin && (
              <ApiKeysTab slug={slug} customer={customer.data} />
            )}
            {activeTab === "webhooks" && isAdmin && (
              <WebhooksTab slug={slug} customer={customer.data} />
            )}
            {activeTab === "sessions" && (
              <SessionsTab
                slug={slug}
                customer={customer.data}
                isAdmin={isAdmin}
              />
            )}
            {activeTab === "settings" && isAdmin && (
              <SettingsTab
                key={customer.data.updatedAt}
                slug={slug}
                customer={customer.data}
              />
            )}
          </div>
        </>
      )}
    </>
  );
}

// The flows assigned to the customer, as cards: what each asks for and how
// sure it is. An admin switches to the assignment editor, which lists every
// flow of the org; a member's list holds only the assigned ones.
function FlowsTab({
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
  const assign = useSetProofingCustomerFlowsMutation(slug, customer.id);
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
      <div className="flex items-center justify-between gap-4">
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
  sessions,
  onEdit,
}: {
  flow: ProofingCustomerFlow;
  paused: boolean;
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
                level:
                  flow.requiredAssuranceLevel.charAt(0).toUpperCase() +
                  flow.requiredAssuranceLevel.slice(1),
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
function SendModal({
  slug,
  customer,
  isAdmin,
  onClose,
}: {
  slug: string;
  customer: ProofingCustomer;
  isAdmin: boolean;
  onClose: () => void;
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
  const save = useSetProofingCustomerFlowsMutation(slug, customerId);
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
      <p className={`${HINT} mt-1`}>
        {t("customers.flows.hint")}{" "}
        <Link
          to={`/${slug}/identity-proofing/flows`}
          className="text-link font-semibold underline"
        >
          {t("identityProofingFlows.title")}
        </Link>
      </p>
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
                        <Tag tone="blue">{flow.requiredAssuranceLevel}</Tag>
                      )}
                      {!flow.completable && (
                        <Tag tone="amber">
                          {t("identityProofingFlows.notCompletable")}
                        </Tag>
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

function SendForm({
  slug,
  customer,
  flows,
  isAdmin,
  onSent,
  onCancel,
}: {
  slug: string;
  customer: ProofingCustomer;
  flows: ProofingCustomerFlow[];
  isAdmin: boolean;
  onSent: () => void;
  onCancel: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateProofingRequestMutation(slug);
  const { sendable, initial } = assignedFlows(flows);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [picked, setPicked] = useState("");
  const [touched, setTouched] = useState(false);
  // A pick that is no longer assigned falls back to the default.
  const flowId = sendable.some((f) => f.id === picked)
    ? picked
    : (initial?.id ?? "");
  const emailMissing = !email.includes("@");

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (emailMissing || flowId === "") {
      return;
    }
    create.mutate(
      {
        customerId: customer.id,
        email: email.trim(),
        name: name.trim(),
        flowId,
      },
      {
        onSuccess: onSent,
      },
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <p className={HINT}>{t("customers.send.hint")}</p>
      {sendable.length === 0 ? (
        <p className="text-ink-soft text-[13px]">
          {isAdmin
            ? t("customers.send.noFlowsAdmin")
            : t("customers.send.noFlowsMember")}
        </p>
      ) : (
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          <div className="flex flex-col gap-1">
            <label htmlFor="proofing-subject-email" className={LABEL}>
              {t("customers.send.email")}
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
              aria-describedby="proofing-subject-name-hint"
              onChange={(event) => setName(event.target.value)}
            />
            <p id="proofing-subject-name-hint" className={HINT}>
              {t("customers.send.nameHint")}
            </p>
          </div>
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
            <Button type="submit" icon="email" loading={create.isPending}>
              {t("customers.send.submit")}
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}

// Every session sent for the customer (an admin's; a member's own), filterable
// by outcome. A row opens to its details: who it went to, who sent it, and why
// it failed.
function SessionsTab({
  slug,
  customer,
  isAdmin,
}: {
  slug: string;
  customer: ProofingCustomer;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useProofingRequestsQuery(slug, customer.id);
  const formatWhen = useWhenFormatter();
  const [filter, setFilter] = useState<SessionFilter>("all");
  const [open, setOpen] = useState<string | null>(null);
  const all = requests.data ?? [];
  const counts = sessionFilterCounts(all);
  const shown = all.filter((r) => matchesSessionFilter(r.status, filter));

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div
          role="group"
          aria-label={t("customers.sessions.filterLabel")}
          className="flex flex-wrap gap-2"
        >
          {SESSION_FILTERS.map((key) => {
            const active = filter === key;
            return (
              <button
                key={key}
                type="button"
                aria-pressed={active}
                onClick={() => setFilter(key)}
                className={[
                  "h-8 rounded-full border px-3 text-[12.5px] font-semibold transition-colors",
                  active
                    ? "border-ink bg-ink text-surface"
                    : "border-line-strong bg-surface text-ink-soft hover:text-ink",
                ].join(" ")}
              >
                {t(`customers.sessions.filters.${key}`, {
                  count: counts[key],
                })}
              </button>
            );
          })}
        </div>
        <p className="text-muted text-[12px]">
          {isAdmin
            ? t("customers.sessions.retention", {
                count: customer.dataRetentionDays,
              })
            : t("customers.sessions.retentionOwn", {
                count: customer.dataRetentionDays,
              })}
        </p>
      </div>
      <Card>
        <Table>
          <Table.Head>
            <Table.HeaderCell>
              {t("customers.sessions.session")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.requests.subject")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("identityProofing.requests.flow")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("customers.sessions.method")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("customers.sessions.result")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("customers.sessions.started")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("customers.sessions.duration")}
            </Table.HeaderCell>
            <Table.HeaderCell aria-hidden="true" />
          </Table.Head>
          <Table.Body>
            {requests.isPending ? (
              <Table.State colSpan={SESSION_COLUMNS}>
                {t("common.loading")}
              </Table.State>
            ) : requests.isError ? (
              <Table.State colSpan={SESSION_COLUMNS}>
                {proofingErrorMessage(requests.error, t)}
              </Table.State>
            ) : shown.length === 0 ? (
              <Table.State colSpan={SESSION_COLUMNS}>
                {t("identityProofing.requests.empty")}
              </Table.State>
            ) : (
              shown.map((request) => (
                <SessionRow
                  key={request.id}
                  slug={slug}
                  request={request}
                  isAdmin={isAdmin}
                  expanded={open === request.id}
                  onToggle={() =>
                    setOpen((current) =>
                      current === request.id ? null : request.id,
                    )
                  }
                  formatWhen={formatWhen}
                />
              ))
            )}
          </Table.Body>
        </Table>
      </Card>
    </>
  );
}

function SessionRow({
  slug,
  request,
  isAdmin,
  expanded,
  onToggle,
  formatWhen,
}: {
  slug: string;
  request: ProofingRequest;
  isAdmin: boolean;
  expanded: boolean;
  onToggle: () => void;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const subject = requestSubject(request);
  const duration = sessionDurationSeconds(request);
  const detailsId = `session-details-${request.id}`;

  return (
    <>
      <Table.Row
        className="hover:bg-surface-2 cursor-pointer transition-colors"
        onClick={onToggle}
      >
        <Table.Cell className="text-ink-soft font-mono text-[12px]">
          {shortRequestId(request.id)}
        </Table.Cell>
        <Table.Cell>
          <div className="max-w-56 truncate font-semibold" title={subject.name}>
            {subject.name}
          </div>
          {subject.verifiedAs && (
            <div className="text-[12px]">
              {t("identityProofing.requests.verifiedAs", {
                name: subject.verifiedAs,
              })}
            </div>
          )}
        </Table.Cell>
        <Table.Cell>{request.flowName}</Table.Cell>
        <Table.Cell className="text-ink-soft whitespace-nowrap">
          {proofingMethodLabel(request.method, t)}
        </Table.Cell>
        <Table.Cell>
          <ResultTag request={request} compact />
        </Table.Cell>
        <Table.Cell className="whitespace-nowrap">
          {formatWhen(request.createdAt)}
        </Table.Cell>
        <Table.Cell className="text-ink-soft">
          {duration === undefined ? "—" : formatDuration(duration)}
        </Table.Cell>
        <Table.Cell className="w-10 text-right">
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={detailsId}
            aria-label={t("customers.sessions.details", {
              id: shortRequestId(request.id),
            })}
            onClick={(event) => {
              event.stopPropagation();
              onToggle();
            }}
            className="text-muted hover:text-ink"
          >
            <Icon
              name={expanded ? "chevron_down" : "chevron_right"}
              size={OPEN_ICON_SIZE}
            />
          </button>
        </Table.Cell>
      </Table.Row>
      {expanded && (
        <tr id={detailsId}>
          <td
            colSpan={SESSION_COLUMNS}
            className="border-line bg-surface-2 border-b px-5 py-4"
          >
            <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
              <dl className="grid content-start gap-x-8 gap-y-2 text-[13px] sm:grid-cols-[auto_1fr]">
                <dt className="text-muted">{t("customers.sessions.method")}</dt>
                <dd>{proofingMethodLabel(request.method, t)}</dd>
                <dt className="text-muted">{t("customers.sessions.fullId")}</dt>
                <dd className="font-mono text-[12px] break-all">
                  {request.id}
                </dd>
                <dt className="text-muted">{t("customers.send.email")}</dt>
                <dd>{request.subjectEmail}</dd>
                {isAdmin && (
                  <>
                    <dt className="text-muted">
                      {t("identityProofing.requests.requestedBy")}
                    </dt>
                    <dd>
                      {request.apiKeyName
                        ? t("customers.sessions.viaApiKey", {
                            name: request.apiKeyName,
                          })
                        : request.requestedByName || "—"}
                    </dd>
                  </>
                )}
                <dt className="text-muted">
                  {t("identityProofing.requests.flow")}
                </dt>
                <dd>
                  {request.flowName}
                  {request.flowVersion !== undefined &&
                    ` ${t("identityProofingFlows.versionShort", {
                      version: request.flowVersion,
                    })}`}
                </dd>
                <dt className="text-muted">
                  {t("identityProofing.requests.assurance")}
                </dt>
                <dd>{request.eidasLevel ?? "—"}</dd>
                {request.errorCode && (
                  <>
                    <dt className="text-muted">
                      {t("customers.sessions.reason")}
                    </dt>
                    <dd>{proofingRejectionReason(request.errorCode, t)}</dd>
                  </>
                )}
                {request.completedAt && (
                  <>
                    <dt className="text-muted">
                      {t("customers.sessions.completed")}
                    </dt>
                    <dd>{formatWhen(request.completedAt)}</dd>
                  </>
                )}
              </dl>
              <SessionTimeline slug={slug} request={request} />
            </div>
          </td>
        </tr>
      )}
    </>
  );
}

// A session's timeline: every audit event about it, oldest first, with who
// acted (a member, the system, or the customer's API) and what it added.
function SessionTimeline({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const events = useProofingRequestEventsQuery(
    slug,
    request.id,
    isProofingLive(request.status),
  );
  const formatWhen = useWhenFormatter();

  return (
    <section aria-labelledby={`timeline-${request.id}`}>
      <h3
        id={`timeline-${request.id}`}
        className="text-muted mb-3 font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase"
      >
        {t("customers.sessions.timeline")}
      </h3>
      {events.isPending ? (
        <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
      ) : events.isError ? (
        <p className={ERROR}>{proofingErrorMessage(events.error, t)}</p>
      ) : events.data.length === 0 ? (
        <p className="text-ink-soft text-[13px]">
          {t("customers.sessions.noEvents")}
        </p>
      ) : (
        <ol className="relative flex flex-col gap-4">
          <span
            aria-hidden="true"
            className="bg-line absolute top-2 bottom-2 left-[13px] w-px"
          />
          {events.data.map((event) => {
            const visual = auditVisual(event.action);
            const detail = sessionEventDetail(event.metadata, t);
            return (
              <li key={event.id} className="relative flex gap-3">
                <span
                  className={`relative inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${AUDIT_TONE_CLASSES[visual.tone]}`}
                >
                  <Icon name={visual.icon} size={TIMELINE_ICON_SIZE} />
                </span>
                <div className="min-w-0 flex-1 pt-0.5">
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                    <span className="text-ink text-[13px] font-semibold">
                      {auditActionLabel(event.action, t)}
                    </span>
                    <time
                      dateTime={event.occurredAt}
                      className="text-muted text-[12px] whitespace-nowrap"
                    >
                      {formatWhen(event.occurredAt)}
                    </time>
                  </div>
                  <div className="text-ink-soft text-[12px]">
                    {[
                      timelineActor(event, request, t, detail.length > 0),
                      ...detail,
                    ]
                      .filter((part) => part !== null)
                      .join(" · ")}
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}

// Who acted: the member, else the customer's API key for the request it
// created. What the proofing service reported has no actor: it is named by its
// detail (the app used, the reason), or as the system's when it has none.
function timelineActor(
  event: AuditEvent,
  request: ProofingRequest,
  t: TFunction,
  hasDetail: boolean,
): string | null {
  if (event.actor) {
    return (
      event.actor.preferredName ??
      `${event.actor.givenNames} ${event.actor.lastName}`.trim()
    );
  }
  if (event.action === "identity_proofing.requested" && request.apiKeyName) {
    return t("customers.sessions.viaApiKey", { name: request.apiKeyName });
  }
  return hasDetail ? null : t("auditLog.system");
}
