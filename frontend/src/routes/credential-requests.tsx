import { useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  IncomingRequest,
  OutboundRequest,
} from "../api/credential-requests";
import {
  useApproveIncomingRequestMutation,
  useDeclineIncomingRequestMutation,
  useIncomingRequestsQuery,
  useOutboundRequestsQuery,
  useSendCredentialRequestMutation,
} from "../api/credential-requests.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useQerdsAddressesQuery,
  useQerdsContactsQuery,
} from "../api/qerds.queries";
import { accessMessage } from "../lib/access-message";
import type {
  CredentialRequestError,
  CredentialRequestErrors,
  CredentialRow,
} from "../lib/credential-request";
import {
  buildSendPayload,
  claimValueText,
  hasErrors,
  MAX_CREDENTIALS,
  outboundStatusTone,
  validateCredentialRequest,
} from "../lib/credential-request";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card, ConfirmDialog, Modal, Tag, TopBar } from "../ui";

const ADMIN_ROLE = "admin";
const FORM_ID = "credential-request-form";

const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

type ControlState = "ok" | "error";

function control(state: ControlState): string {
  return [CONTROL, state === "error" ? CONTROL_ERR : CONTROL_OK].join(" ");
}

function controlState(error: unknown): ControlState {
  return error ? "error" : "ok";
}

type Tab = "incoming" | "sent";
const TABS: readonly Tab[] = ["incoming", "sent"];

function readTab(params: URLSearchParams): Tab {
  const value = params.get("tab");
  return TABS.find((tab) => tab === value) ?? TABS[0];
}

// Credential requests between organizations over QERDS: what other
// organizations ask this one for (approve or decline), and what this one asked
// others for (with the verified answer). Admin-only on the backend; a member
// sees the access message.
export default function CredentialRequests(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === ADMIN_ROLE;
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = readTab(searchParams);
  const [composing, setComposing] = useState(false);

  const enabled = isAdmin;
  const incoming = useIncomingRequestsQuery(slug, enabled);
  const outbound = useOutboundRequestsQuery(slug, enabled);

  const setTab = (value: Tab): void => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === TABS[0]) next.delete("tab");
      else next.set("tab", value);
      return next;
    });
  };

  const labels: Record<Tab, string> = {
    incoming: t("credentialRequests.tabs.incoming"),
    sent: t("credentialRequests.tabs.sent"),
  };

  return (
    <>
      <TopBar
        title={t("credentialRequests.title")}
        subtitle={t("credentialRequests.subtitle")}
        actions={
          isAdmin ? (
            <Button icon="add" onClick={() => setComposing(true)}>
              {t("credentialRequests.newRequest")}
            </Button>
          ) : undefined
        }
      />

      <div className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8">
        {TABS.map((value) => {
          const active = tab === value;
          return (
            <button
              key={value}
              type="button"
              onClick={() => setTab(value)}
              className={[
                "h-11 shrink-0 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
                active
                  ? "border-primary text-ink font-semibold"
                  : "text-ink-soft hover:text-ink border-transparent font-medium",
              ].join(" ")}
            >
              {labels[value]}
              {value === "incoming" && (incoming.data?.length ?? 0) > 0 && (
                <span className="text-muted ml-1.5 text-[12px]">
                  {incoming.data?.length}
                </span>
              )}
            </button>
          );
        })}
      </div>

      <div className="p-4 sm:p-8">
        {org.isError ? (
          <MessageCard message={accessMessage(org.error, t)} error />
        ) : org.isPending ? (
          <MessageCard message={t("common.loading")} />
        ) : !isAdmin ? (
          <MessageCard message={t("credentialRequests.adminOnly")} />
        ) : tab === "incoming" ? (
          <IncomingTab
            slug={slug}
            requests={incoming.data ?? []}
            pending={incoming.isPending}
            error={incoming.error}
          />
        ) : (
          <SentTab
            requests={outbound.data ?? []}
            pending={outbound.isPending}
            error={outbound.error}
          />
        )}
      </div>

      {composing && (
        <RequestForm
          slug={slug}
          orgName={org.data?.name ?? ""}
          onClose={() => setComposing(false)}
          onSent={() => {
            setComposing(false);
            setTab("sent");
          }}
        />
      )}
    </>
  );
}

function MessageCard({
  message,
  error = false,
}: {
  message: string;
  error?: boolean;
}): React.JSX.Element {
  return (
    <Card className="p-6">
      <p className={`${error ? "text-error" : "text-ink-soft"} text-[14px]`}>
        {message}
      </p>
    </Card>
  );
}

function IncomingTab({
  slug,
  requests,
  pending,
  error,
}: {
  slug: string;
  requests: IncomingRequest[];
  pending: boolean;
  error: Error | null;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const approve = useApproveIncomingRequestMutation(slug);
  const decline = useDeclineIncomingRequestMutation(slug);
  const [confirm, setConfirm] = useState<{
    kind: "approve" | "decline";
    request: IncomingRequest;
  } | null>(null);
  const busy = approve.isPending || decline.isPending;

  if (error) {
    return (
      <MessageCard
        message={t("credentialRequests.loadError", { message: error.message })}
        error
      />
    );
  }
  if (pending) {
    return <MessageCard message={t("common.loading")} />;
  }

  return (
    <section className="flex flex-col gap-3">
      <p className="text-ink-soft text-[13px]">
        {t("credentialRequests.incoming.description")}
      </p>
      {requests.length === 0 ? (
        <MessageCard message={t("credentialRequests.incoming.empty")} />
      ) : (
        requests.map((request) => (
          <Card key={request.id} className="flex flex-col gap-3 p-4">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <div className="text-ink truncate font-semibold">
                  {request.verifier}
                </div>
                <div className="text-ink-soft text-[12.5px]">
                  {t("credentialRequests.incoming.expires", {
                    when: formatWhen(request.expiresAt),
                  })}
                </div>
              </div>
              <Tag tone="amber" dot>
                {t("credentialRequests.incoming.pending")}
              </Tag>
            </div>
            <div className="flex flex-col gap-2">
              {request.credentials.map((credential, index) => (
                <div
                  key={index}
                  className="border-line rounded-yivi border p-3"
                >
                  <div className="text-ink font-mono text-[12.5px]">
                    {credential.vcts.join(" / ")}
                  </div>
                  <div className="text-ink-soft mt-1 text-[12.5px]">
                    {credential.claims.length > 0
                      ? t("credentialRequests.incoming.shares", {
                          claims: credential.claims.join(", "),
                        })
                      : t("credentialRequests.incoming.sharesNoClaims")}
                  </div>
                </div>
              ))}
            </div>
            <div className="flex items-center justify-end gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={busy}
                onClick={() => setConfirm({ kind: "decline", request })}
              >
                {t("credentialRequests.incoming.decline")}
              </Button>
              <Button
                size="sm"
                loading={
                  approve.isPending && approve.variables?.id === request.id
                }
                disabled={busy}
                onClick={() => setConfirm({ kind: "approve", request })}
              >
                {t("credentialRequests.incoming.approve")}
              </Button>
            </div>
          </Card>
        ))
      )}

      {confirm && (
        <ConfirmDialog
          title={
            confirm.kind === "approve"
              ? t("credentialRequests.incoming.approve")
              : t("credentialRequests.incoming.decline")
          }
          message={
            confirm.kind === "approve"
              ? t("credentialRequests.incoming.confirmApprove", {
                  verifier: confirm.request.verifier,
                })
              : t("credentialRequests.incoming.confirmDecline", {
                  verifier: confirm.request.verifier,
                })
          }
          confirmLabel={
            confirm.kind === "approve"
              ? t("credentialRequests.incoming.approve")
              : t("credentialRequests.incoming.decline")
          }
          busy={busy}
          onConfirm={() => {
            const id = confirm.request.id;
            if (confirm.kind === "approve") approve.mutate({ id });
            else decline.mutate({ id });
            setConfirm(null);
          }}
          onClose={() => setConfirm(null)}
        />
      )}
    </section>
  );
}

function SentTab({
  requests,
  pending,
  error,
}: {
  requests: OutboundRequest[];
  pending: boolean;
  error: Error | null;
}): React.JSX.Element {
  const { t } = useTranslation();
  if (error) {
    return (
      <MessageCard
        message={t("credentialRequests.loadError", { message: error.message })}
        error
      />
    );
  }
  if (pending) {
    return <MessageCard message={t("common.loading")} />;
  }
  if (requests.length === 0) {
    return <MessageCard message={t("credentialRequests.sent.empty")} />;
  }
  return (
    <section className="flex flex-col gap-3">
      {requests.map((request) => (
        <SentCard key={request.id} request={request} />
      ))}
    </section>
  );
}

function SentCard({
  request,
}: {
  request: OutboundRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const statusLabels: Record<OutboundRequest["status"], string> = {
    sent: t("credentialRequests.sent.status.sent"),
    completed: t("credentialRequests.sent.status.completed"),
    failed: t("credentialRequests.sent.status.failed"),
    expired: t("credentialRequests.sent.status.expired"),
  };
  const failureLabels: Record<string, string> = {
    qerds_send_failed: t("credentialRequests.sent.failure.qerdsSendFailed"),
    verification_failed: t(
      "credentialRequests.sent.failure.verificationFailed",
    ),
    incomplete_response: t(
      "credentialRequests.sent.failure.incompleteResponse",
    ),
  };

  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="text-ink truncate font-semibold">
            {t("credentialRequests.sent.to", { recipient: request.recipient })}
          </div>
          <div className="text-ink-soft text-[12.5px]">
            {formatWhen(request.createdAt)}
            {request.status === "sent" &&
              ` · ${
                request.fetched
                  ? t("credentialRequests.sent.fetched")
                  : t("credentialRequests.sent.notFetched")
              }`}
          </div>
        </div>
        <Tag tone={outboundStatusTone(request.status)} dot>
          {statusLabels[request.status]}
        </Tag>
      </div>

      {request.status === "failed" && request.failureReason && (
        <p className="text-error text-[12.5px]">
          {failureLabels[request.failureReason] ?? request.failureReason}
        </p>
      )}

      <div className="flex flex-col gap-2">
        {request.credentials.map((credential) => {
          const disclosed = request.disclosed.find(
            (d) => d.queryId === credential.id,
          );
          return (
            <div
              key={credential.id}
              className="border-line rounded-yivi border p-3"
            >
              <div className="text-ink font-mono text-[12.5px]">
                {credential.vct}
              </div>
              {disclosed ? (
                <dl className="mt-2 grid grid-cols-[minmax(0,max-content)_1fr] gap-x-4 gap-y-1 text-[12.5px]">
                  {Object.entries(disclosed.claims).map(([name, value]) => (
                    <React.Fragment key={name}>
                      <dt className="text-ink-soft">{name}</dt>
                      <dd className="text-ink break-all">
                        {claimValueText(value)}
                      </dd>
                    </React.Fragment>
                  ))}
                  <dt className="text-muted">
                    {t("credentialRequests.sent.issuer")}
                  </dt>
                  <dd className="text-muted break-all">{disclosed.issuer}</dd>
                </dl>
              ) : (
                <div className="text-ink-soft mt-1 text-[12.5px]">
                  {credential.claims.length > 0
                    ? t("credentialRequests.sent.asked", {
                        claims: credential.claims.join(", "),
                      })
                    : t("credentialRequests.sent.noClaims")}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </Card>
  );
}

const EMPTY_ROW: CredentialRow = { vct: "", claims: "" };

function RequestForm({
  slug,
  orgName,
  onClose,
  onSent,
}: {
  slug: string;
  orgName: string;
  onClose: () => void;
  onSent: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const addresses = useQerdsAddressesQuery(slug);
  const contacts = useQerdsContactsQuery(slug);
  const send = useSendCredentialRequestMutation(slug);
  const [from, setFrom] = useState("");
  const [recipient, setRecipient] = useState("");
  const [rows, setRows] = useState<CredentialRow[]>([EMPTY_ROW]);
  const [errors, setErrors] = useState<CredentialRequestErrors>({ rows: [] });

  const errorText = (error?: CredentialRequestError): string | undefined =>
    error && t(`credentialRequests.form.errors.${error}`);

  const updateRow = (index: number, patch: Partial<CredentialRow>): void => {
    setRows((prev) =>
      prev.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    );
  };

  const handleSubmit = (event: React.FormEvent): void => {
    event.preventDefault();
    const next = validateCredentialRequest(recipient, rows);
    setErrors(next);
    if (hasErrors(next)) return;
    send.mutate(buildSendPayload(from, recipient, rows), { onSuccess: onSent });
  };

  return (
    <Modal
      title={t("credentialRequests.form.title")}
      closeLabel={t("common.cancel")}
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            type="submit"
            form={FORM_ID}
            icon="email"
            loading={send.isPending}
          >
            {t("credentialRequests.form.send")}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        onSubmit={handleSubmit}
        className="flex flex-col gap-4"
        noValidate
      >
        {(addresses.data?.length ?? 0) > 1 && (
          <div className="flex flex-col gap-1">
            <label htmlFor="credential-request-from" className={FIELD_LABEL}>
              {t("credentialRequests.form.from")}
            </label>
            <select
              id="credential-request-from"
              className={`${control("ok")} h-9`}
              value={from}
              onChange={(event) => setFrom(event.target.value)}
            >
              <option value="">
                {t("credentialRequests.form.fromDefault")}
              </option>
              {addresses.data?.map((address) => (
                <option key={address.id} value={address.address}>
                  {address.address}
                </option>
              ))}
            </select>
          </div>
        )}

        <div className="flex flex-col gap-1">
          <label htmlFor="credential-request-recipient" className={FIELD_LABEL}>
            {t("credentialRequests.form.recipient")}
            <span aria-hidden className="text-error ml-0.5">
              *
            </span>
          </label>
          <input
            id="credential-request-recipient"
            className={`${control(controlState(errors.recipient))} h-9`}
            value={recipient}
            onChange={(event) => setRecipient(event.target.value)}
            placeholder={t("credentialRequests.form.recipientPlaceholder")}
            list="credential-request-contacts"
            aria-required
            aria-invalid={errors.recipient ? true : undefined}
            autoFocus
          />
          <datalist id="credential-request-contacts">
            {contacts.data?.map((contact) => (
              <option key={contact.id} value={contact.address}>
                {contact.name}
              </option>
            ))}
          </datalist>
          {errors.recipient && (
            <span role="alert" className="text-error text-[12px]">
              {errorText(errors.recipient)}
            </span>
          )}
        </div>

        <div className="flex flex-col gap-3">
          <span className={FIELD_LABEL}>
            {t("credentialRequests.form.credentials")}
          </span>
          {rows.map((row, index) => (
            <div
              key={index}
              className="border-line rounded-yivi flex flex-col gap-2 border p-3"
            >
              <div className="flex items-center gap-2">
                <input
                  className={`${control(controlState(errors.rows[index]))} h-9 font-mono`}
                  value={row.vct}
                  onChange={(event) =>
                    updateRow(index, { vct: event.target.value })
                  }
                  placeholder={t("credentialRequests.form.vctPlaceholder")}
                  aria-label={t("credentialRequests.form.vct")}
                  aria-invalid={errors.rows[index] ? true : undefined}
                />
                {rows.length > 1 && (
                  <Button
                    variant="dangerGhost"
                    icon="delete"
                    iconOnly
                    aria-label={t("credentialRequests.form.removeCredential")}
                    onClick={() =>
                      setRows((prev) => prev.filter((_, i) => i !== index))
                    }
                  />
                )}
              </div>
              <input
                className={`${control("ok")} h-9`}
                value={row.claims}
                onChange={(event) =>
                  updateRow(index, { claims: event.target.value })
                }
                placeholder={t("credentialRequests.form.claimsPlaceholder")}
                aria-label={t("credentialRequests.form.claims")}
              />
              {errors.rows[index] && (
                <span role="alert" className="text-error text-[12px]">
                  {errorText(errors.rows[index])}
                </span>
              )}
            </div>
          ))}
          {rows.length < MAX_CREDENTIALS && (
            <div>
              <Button
                variant="ghost"
                size="sm"
                icon="add"
                onClick={() => setRows((prev) => [...prev, EMPTY_ROW])}
              >
                {t("credentialRequests.form.addCredential")}
              </Button>
            </div>
          )}
        </div>

        <p className="text-ink-soft text-[12.5px]">
          {t("credentialRequests.form.note", { org: orgName })}
        </p>
      </form>
    </Modal>
  );
}
