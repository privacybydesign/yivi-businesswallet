import { useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateRequestMutation,
  useProofingFlowsQuery,
  useMemberRequestsQuery,
} from "../api/identity-proofing.queries";
import { useWhenFormatter } from "../lib/format-when";
import {
  proofingErrorMessage,
  proofingStatusLabel,
  proofingStatusTone,
  sendableByMail,
  sendableFlows,
} from "../lib/identity-proofing";
import { Button, Tag } from "../ui";

const ERROR = "text-error text-[12px]";
// Mirrors the Input base so the flow select reads as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-9 disabled:opacity-60";

// Identity proofing for one member, on their detail page: a flow picker over
// the flows an admin made available to members, a button that mails the member
// a proofing request on it, and the outcome of the last request sent to them.
export function MemberProofing({
  slug,
  userId,
}: {
  slug: string;
  userId: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const flows = useProofingFlowsQuery(slug);
  // Asked for this member alone: the org-wide list is capped, so a member whose
  // last request is older than its newest entries would show none.
  const requests = useMemberRequestsQuery(slug, userId);
  const create = useCreateRequestMutation(slug);
  // A member is only ever mailed, so a flow that needs the browser page for
  // diploma uploads is not offered (nor started on as the default).
  const { sendable, initial } = sendableFlows(
    (flows.data ?? []).filter(sendableByMail),
  );
  // Unset, or a flow no longer available, falls back to the admin's default.
  const [picked, setPicked] = useState("");
  const flowId = sendable.some((f) => f.id === picked)
    ? picked
    : (initial?.id ?? "");
  const latest = requests.data?.at(0);
  const selectId = `member-proofing-flow-${userId}`;

  if (flows.isError) {
    return <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>;
  }

  return (
    <>
      <label htmlFor={selectId} className="text-ink-soft text-[12px]">
        {t("memberDetail.proofing.flow")}
      </label>
      <select
        id={selectId}
        className={SELECT_CLASS}
        value={flowId}
        disabled={flows.isPending || sendable.length === 0}
        onChange={(event) => setPicked(event.target.value)}
      >
        {sendable.map((flow) => (
          <option key={flow.id} value={flow.id}>
            {flow.name}
          </option>
        ))}
      </select>
      <Button
        variant="secondary"
        icon="scan_qrcode"
        className="w-full"
        loading={create.isPending}
        disabled={flowId === ""}
        onClick={() => create.mutate({ userId, flowId })}
      >
        {t("memberDetail.proofing.send")}
      </Button>
      {!flows.isPending && sendable.length === 0 ? (
        <p className="text-ink-soft text-[12px]">
          {t("memberDetail.proofing.noFlows")}{" "}
          <Link
            to={`/${slug}/identity-proofing/flows`}
            className="text-link font-semibold underline"
          >
            {t("identityProofingFlows.title")}
          </Link>
        </p>
      ) : (
        <p className="text-ink-soft text-[12px]">
          {t("memberDetail.proofing.hint")}
        </p>
      )}
      {create.isError && (
        <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
      )}
      {latest && (
        <div className="flex flex-wrap items-center gap-2 text-[12px]">
          <span className="text-ink-soft">
            {t("memberDetail.proofing.lastRequest")}
          </span>
          <Tag tone={proofingStatusTone(latest.status)} dot>
            {proofingStatusLabel(latest.status, t)}
          </Tag>
          <span className="text-ink-soft">
            {latest.flowName} · {formatWhen(latest.createdAt)}
          </span>
        </div>
      )}
    </>
  );
}
