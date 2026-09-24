import { useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useCreateProofingRequestMutation,
  useProofingFlowsQuery,
  useProofingMembersQuery,
  useProofingRequestsQuery,
} from "../api/identity-proofing.queries";
import type {
  ProofingFlow,
  ProofingMember,
  ProofingRequest,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  latestRequestByMember,
  proofingErrorMessage,
  proofingStatusLabel,
  proofingStatusTone,
  sendableFlows,
} from "../lib/identity-proofing";
import { Button, Card, Input, Table, Tag, TopBar } from "../ui";

const ERROR = "text-error text-[12.5px]";
const MEMBER_COLUMNS = 4;
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full min-w-[12rem] border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

// The org's identity proofing page (privacybydesign/identity-proofing-service):
// every member of the org, admins and externals included, with a flow picker
// and a mail button that sends them a proofing request. The mail carries a QR
// code and a button for one link that lives as long as the proofing session
// (15 minutes). An admin sees every request's status; a member the ones they sent.
export default function IdentityProofing(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const flows = useProofingFlowsQuery(slug);

  return (
    <>
      <TopBar
        title={t("identityProofing.title")}
        subtitle={t("identityProofing.subtitle")}
      />
      <div className="flex flex-col gap-6 p-8">
        {flows.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : flows.isError ? (
          <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>
        ) : (
          <>
            <MembersCard slug={slug} isAdmin={isAdmin} flows={flows.data} />
            <RequestsTable slug={slug} isAdmin={isAdmin} />
          </>
        )}
      </div>
    </>
  );
}

function MembersCard({
  slug,
  isAdmin,
  flows,
}: {
  slug: string;
  isAdmin: boolean;
  flows: ProofingFlow[];
}): React.JSX.Element {
  const { t } = useTranslation();
  const members = useProofingMembersQuery(slug);
  const requests = useProofingRequestsQuery(slug);
  const create = useCreateProofingRequestMutation(slug);
  const { sendable, initial } = sendableFlows(flows);
  const [query, setQuery] = useState("");
  // The flow picked per member; unset rows use the admin's default.
  const [picked, setPicked] = useState<Record<string, string>>({});

  const latest = useMemo(
    () => latestRequestByMember(requests.data ?? []),
    [requests.data],
  );
  const needle = query.trim().toLowerCase();
  const shown = (members.data ?? []).filter(
    (m) =>
      needle === "" ||
      m.name.toLowerCase().includes(needle) ||
      m.email.toLowerCase().includes(needle),
  );

  function flowFor(userId: string): string {
    const choice = picked[userId];
    return choice && sendable.some((f) => f.id === choice)
      ? choice
      : (initial?.id ?? "");
  }

  return (
    <Card>
      <div className="flex flex-wrap items-end justify-between gap-4 px-6 pt-5 pb-3">
        <div>
          <h2 className="font-display text-[16px] font-bold">
            {t("identityProofing.members.title")}
          </h2>
          <p className="text-ink-soft mt-1 text-[12px]">
            {t("identityProofing.members.hint")}
          </p>
        </div>
        <div className="w-full max-w-xs">
          <Input
            type="search"
            value={query}
            placeholder={t("identityProofing.members.search")}
            aria-label={t("identityProofing.members.search")}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
      </div>
      {sendable.length === 0 && (
        <p className="text-ink-soft px-6 pb-3 text-[13px]">
          {isAdmin ? (
            <>
              {t("identityProofing.members.noFlowsAdmin")}{" "}
              <Link
                to={`/${slug}/identity-proofing/flows`}
                className="text-link font-semibold underline"
              >
                {t("identityProofingFlows.title")}
              </Link>
            </>
          ) : (
            t("identityProofing.members.noFlowsMember")
          )}
        </p>
      )}
      {create.isError && (
        <p className={`${ERROR} px-6 pb-3`}>
          {proofingErrorMessage(create.error, t)}
        </p>
      )}
      <Table>
        <Table.Head>
          <Table.HeaderCell>
            {t("identityProofing.members.member")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.members.lastRequest")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            {t("identityProofing.members.flow")}
          </Table.HeaderCell>
          <Table.HeaderCell>
            <span className="sr-only">
              {t("identityProofing.members.send")}
            </span>
          </Table.HeaderCell>
        </Table.Head>
        <Table.Body>
          {members.isPending ? (
            <Table.State colSpan={MEMBER_COLUMNS}>
              {t("common.loading")}
            </Table.State>
          ) : members.isError ? (
            <Table.State colSpan={MEMBER_COLUMNS}>
              {proofingErrorMessage(members.error, t)}
            </Table.State>
          ) : shown.length === 0 ? (
            <Table.State colSpan={MEMBER_COLUMNS}>
              {t("identityProofing.members.empty")}
            </Table.State>
          ) : (
            shown.map((member) => (
              <MemberRow
                key={member.userId}
                member={member}
                latest={latest.get(member.userId)}
                sendable={sendable}
                flowId={flowFor(member.userId)} // here the drop down
                onPick={(flowId) =>
                  setPicked((current) => ({
                    ...current,
                    [member.userId]: flowId,
                  }))
                }
                sending={
                  create.isPending && create.variables.userId === member.userId
                }
                onSend={(flowId) =>
                  create.mutate({ userId: member.userId, flowId })
                }
              />
            ))
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}

function MemberRow({
  member,
  latest,
  sendable,
  flowId,
  onPick,
  sending,
  onSend,
}: {
  member: ProofingMember;
  latest: ProofingRequest | undefined;
  sendable: ProofingFlow[];
  flowId: string;
  onPick: (flowId: string) => void;
  sending: boolean;
  onSend: (flowId: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const selectId = `proofing-flow-${member.userId}`;

  return (
    <Table.Row>
      <Table.Cell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-semibold">{member.name}</span>
          {member.role === "admin" && (
            <Tag tone="blue">{t("identityProofing.members.admin")}</Tag>
          )}
          {member.memberType === "external" && (
            <Tag>
              {member.externalOrganisation
                ? t("identityProofing.members.externalOf", {
                    org: member.externalOrganisation,
                  })
                : t("identityProofing.members.external")}
            </Tag>
          )}
        </div>
        <div className="text-ink-soft text-[12px]">{member.email}</div>
      </Table.Cell>
      <Table.Cell>
        {latest ? (
          <div className="flex flex-col gap-1">
            <span>
              <Tag tone={proofingStatusTone(latest.status)} dot>
                {proofingStatusLabel(latest.status, t)}
              </Tag>
            </span>
            <span className="text-ink-soft text-[12px]">
              {latest.flowName} · {formatWhen(latest.createdAt)}
            </span>
          </div>
        ) : (
          <span className="text-ink-soft">—</span>
        )}
      </Table.Cell>
      <Table.Cell>
        <label htmlFor={selectId} className="sr-only">
          {t("identityProofing.members.flowFor", { name: member.name })}
        </label>
        <select
          id={selectId}
          className={SELECT_CLASS}
          value={flowId}
          disabled={sendable.length === 0}
          onChange={(event) => onPick(event.target.value)}
        >
          {sendable.map((flow) => (
            <option key={flow.id} value={flow.id}>
              {flow.name}
            </option>
          ))}
        </select>
      </Table.Cell>
      <Table.Cell>
        <Button
          variant="secondary"
          size="sm"
          icon="email"
          iconOnly
          aria-label={t("identityProofing.members.sendTo", {
            name: member.name,
          })}
          title={t("identityProofing.members.sendTo", { name: member.name })}
          loading={sending}
          disabled={flowId === ""}
          onClick={() => onSend(flowId)}
        />
      </Table.Cell>
    </Table.Row>
  );
}

function RequestsTable({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const requests = useProofingRequestsQuery(slug);
  const formatWhen = useWhenFormatter();
  const columns = isAdmin ? 6 : 5;

  return (
    <Card>
      <h2 className="font-display px-6 pt-5 pb-3 text-[16px] font-bold">
        {isAdmin
          ? t("identityProofing.requests.title")
          : t("identityProofing.requests.titleOwn")}
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
            requests.data.map((request) => (
              <Table.Row key={request.id}>
                <Table.Cell>
                  <div className="font-semibold">{request.subjectName}</div>
                  <div className="text-ink-soft text-[12px]">
                    {request.subjectEmail}
                  </div>
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
                {isAdmin && <Table.Cell>{request.requestedByName}</Table.Cell>}
                <Table.Cell>
                  <Tag tone={proofingStatusTone(request.status)} dot>
                    {proofingStatusLabel(request.status, t)}
                  </Tag>
                </Table.Cell>
                <Table.Cell>{request.eidasLevel ?? "—"}</Table.Cell>
                <Table.Cell>{formatWhen(request.createdAt)}</Table.Cell>
              </Table.Row>
            ))
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}
