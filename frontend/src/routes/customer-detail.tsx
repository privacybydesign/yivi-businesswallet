import { useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useCreateProofingRequestMutation,
  useProofingCustomerFlowsQuery,
  useProofingCustomerQuery,
  useProofingRequestsQuery,
  useRenameProofingCustomerMutation,
  useSetProofingCustomerFlowsMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingCustomer,
  ProofingCustomerFlow,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  assignedFlows,
  editedFlowSelection,
  proofingErrorMessage,
  proofingStatusLabel,
  proofingStatusTone,
  requestSubject,
} from "../lib/identity-proofing";
import { Button, Card, Input, Table, Tag, TopBar } from "../ui";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const HTTP_NOT_FOUND = 404;
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

// One customer of the org: the flows an admin assigned to it, a form any member
// uses to verify a person for it (an e-mail address and an optional name; the
// person needs no account), and the requests sent for it. An admin sees every
// request for the customer, a member the ones they sent.
export default function CustomerDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, customerId } = useParams();
  // Guaranteed by the ":orgSlug/…/:customerId" route this component mounts under.
  const slug = orgSlug!;
  const id = customerId!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const customer = useProofingCustomerQuery(slug, id);
  const [renaming, setRenaming] = useState(false);

  const notFound =
    customer.error instanceof ApiError &&
    customer.error.status === HTTP_NOT_FOUND;

  return (
    <>
      <TopBar
        title={customer.data?.name ?? t("customers.title")}
        subtitle={t("customers.subtitle")}
        actions={
          isAdmin &&
          customer.data &&
          !renaming && (
            <Button
              variant="secondary"
              icon="edit"
              onClick={() => setRenaming(true)}
            >
              {t("customers.detail.rename")}
            </Button>
          )
        }
      />
      <div className="flex flex-col gap-6 p-8">
        <Link
          to={`/${slug}/customers`}
          className="text-link text-[13px] font-semibold underline"
        >
          {t("customers.detail.back")}
        </Link>
        {customer.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : customer.isError ? (
          <p className={ERROR}>
            {notFound
              ? t("customers.detail.notFound")
              : proofingErrorMessage(customer.error, t)}
          </p>
        ) : (
          <>
            {renaming && (
              <RenameCard
                slug={slug}
                customer={customer.data}
                onDone={() => setRenaming(false)}
              />
            )}
            <CustomerFlows
              slug={slug}
              customer={customer.data}
              isAdmin={isAdmin}
            />
            <RequestsCard slug={slug} customerId={id} isAdmin={isAdmin} />
          </>
        )}
      </div>
    </>
  );
}

function RenameCard({
  slug,
  customer,
  onDone,
}: {
  slug: string;
  customer: ProofingCustomer;
  onDone: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const rename = useRenameProofingCustomerMutation(slug, customer.id);
  const [name, setName] = useState(customer.name);
  const trimmed = name.trim();

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    if (trimmed === "") {
      return;
    }
    rename.mutate(trimmed, { onSuccess: onDone });
  }

  return (
    <Card className="p-6">
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={submit}
        noValidate
      >
        <div className="flex w-full max-w-sm flex-col gap-1">
          <label htmlFor="proofing-customer-rename" className={LABEL}>
            {t("customers.detail.name")}
          </label>
          <Input
            id="proofing-customer-rename"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </div>
        <Button
          type="submit"
          loading={rename.isPending}
          disabled={trimmed === "" || trimmed === customer.name}
        >
          {t("customers.detail.save")}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t("customers.detail.cancel")}
        </Button>
      </form>
      {rename.isError && (
        <p className={`${ERROR} mt-2`}>
          {proofingErrorMessage(rename.error, t)}
        </p>
      )}
    </Card>
  );
}

// The customer's flows feed both the admin's assignment and the send form: an
// admin's list holds every flow of the org, a member's only the assigned ones.
function CustomerFlows({
  slug,
  customer,
  isAdmin,
}: {
  slug: string;
  customer: ProofingCustomer;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const flows = useProofingCustomerFlowsQuery(slug, customer.id);

  if (flows.isPending) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  if (flows.isError) {
    return <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>;
  }
  return (
    <>
      {isAdmin && (
        <AssignedFlowsCard
          // Reseeded whenever the saved assignment changes.
          key={`${customer.flowIds.join(",")}@${customer.defaultFlowId ?? ""}`}
          slug={slug}
          customerId={customer.id}
          flows={flows.data}
        />
      )}
      <SendCard
        slug={slug}
        customer={customer}
        flows={flows.data}
        isAdmin={isAdmin}
      />
    </>
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

function SendCard({
  slug,
  customer,
  flows,
  isAdmin,
}: {
  slug: string;
  customer: ProofingCustomer;
  flows: ProofingCustomerFlow[];
  isAdmin: boolean;
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
        onSuccess: () => {
          setEmail("");
          setName("");
          setTouched(false);
        },
      },
    );
  }

  return (
    <Card className="p-6">
      <h2 className="font-display text-[16px] font-bold">
        {t("customers.send.title")}
      </h2>
      <p className={`${HINT} mt-1`}>{t("customers.send.hint")}</p>
      {sendable.length === 0 ? (
        <p className="text-ink-soft mt-4 text-[13px]">
          {isAdmin
            ? t("customers.send.noFlowsAdmin")
            : t("customers.send.noFlowsMember")}
        </p>
      ) : (
        <form
          className="mt-4 grid gap-4 md:grid-cols-3"
          onSubmit={submit}
          noValidate
        >
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
            <p className={`${ERROR} md:col-span-3`}>
              {proofingErrorMessage(create.error, t)}
            </p>
          )}
          <div className="md:col-span-3">
            <Button type="submit" icon="email" loading={create.isPending}>
              {t("customers.send.submit")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}

function RequestsCard({
  slug,
  customerId,
  isAdmin,
}: {
  slug: string;
  customerId: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useProofingRequestsQuery(slug, customerId);
  const formatWhen = useWhenFormatter();
  const columns = isAdmin ? 6 : 5;

  return (
    <Card>
      <h2 className="font-display px-6 pt-5 pb-3 text-[16px] font-bold">
        {isAdmin
          ? t("customers.requests.title")
          : t("customers.requests.titleOwn")}
      </h2>
      <Table>
        <Table.Head>
          <Table.HeaderCell>
            {t("identityProofing.requests.subject")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.requests.flow")}
          </Table.HeaderCell>
          {isAdmin && (
            <Table.HeaderCell>
              {t("identityProofing.requests.requestedBy")}
            </Table.HeaderCell>
          )}
          <Table.HeaderCell>
            {t("identityProofing.requests.status")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.requests.assurance")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.requests.created")}
          </Table.HeaderCell>
        </Table.Head>
        <Table.Body>
          {requests.isPending ? (
            <Table.State colSpan={columns}>{t("common.loading")}</Table.State>
          ) : requests.isError ? (
            <Table.State colSpan={columns}>
              {proofingErrorMessage(requests.error, t)}
            </Table.State>
          ) : requests.data.length === 0 ? (
            <Table.State colSpan={columns}>
              {t("identityProofing.requests.empty")}
            </Table.State>
          ) : (
            requests.data.map((request) => {
              const subject = requestSubject(request);
              return (
                <Table.Row key={request.id}>
                  <Table.Cell>
                    <div className="font-semibold">{subject.name}</div>
                    {subject.verifiedAs && (
                      <div className="text-[12px]">
                        {t("identityProofing.requests.verifiedAs", {
                          name: subject.verifiedAs,
                        })}
                      </div>
                    )}
                    {subject.name !== request.subjectEmail && (
                      <div className="text-ink-soft text-[12px]">
                        {request.subjectEmail}
                      </div>
                    )}
                  </Table.Cell>
                  <Table.Cell>
                    {request.flowName}
                    {request.flowVersion !== undefined && (
                      <span className="text-ink-soft text-[12px]">
                        {" "}
                        {t("identityProofingFlows.versionShort", {
                          version: request.flowVersion,
                        })}
                      </span>
                    )}
                  </Table.Cell>
                  {isAdmin && (
                    <Table.Cell>{request.requestedByName}</Table.Cell>
                  )}
                  <Table.Cell>
                    <Tag tone={proofingStatusTone(request.status)} dot>
                      {proofingStatusLabel(request.status, t)}
                    </Tag>
                  </Table.Cell>
                  <Table.Cell>{request.eidasLevel ?? "—"}</Table.Cell>
                  <Table.Cell>{formatWhen(request.createdAt)}</Table.Cell>
                </Table.Row>
              );
            })
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}
