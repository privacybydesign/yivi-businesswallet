import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQerdsMessageQuery } from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { downloadQerdsAttachment } from "../api/qerds";
import type { QerdsAttachment, QerdsEvidence } from "../api/qerds";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import { decodeEvidence, formatBytes, qerdsStatusTone } from "../lib/qerds";
import { toast } from "../lib/toast";
import { Button, Card, Icon, Tag, TopBar } from "../ui";
import * as React from "react";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";

function evidenceTypeLabel(type: string, t: TFunction): string {
  switch (type) {
    case "submission-acceptance":
      return t("qerds.evidence.types.submission");
    case "delivery":
      return t("qerds.evidence.types.delivery");
    case "relay":
      return t("qerds.evidence.types.relay");
    case "non-delivery":
      return t("qerds.evidence.types.nonDelivery");
    default:
      return type;
  }
}

function DetailRow({
  label,
  value,
  mono,
  capitalize,
}: {
  label: string;
  value: string;
  mono?: boolean;
  capitalize?: boolean;
}): React.JSX.Element {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className={EYEBROW}>{label}</span>
      <span
        className={[
          "text-ink truncate text-[13px] font-medium",
          mono ? "font-mono text-[12px]" : "",
          capitalize ? "capitalize" : "",
        ].join(" ")}
      >
        {value}
      </span>
    </div>
  );
}

function EvidenceItem({
  evidence,
  dateFormatter,
  t,
}: {
  evidence: QerdsEvidence;
  dateFormatter: Intl.DateTimeFormat;
  t: TFunction;
}): React.JSX.Element {
  return (
    <li className="flex gap-3">
      <span className="text-valid inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-[color-mix(in_srgb,var(--color-valid)_14%,transparent)]">
        <Icon name="valid" size={15} />
      </span>
      <div className="flex-1 pb-5">
        <div className="text-ink text-[13.5px] font-semibold">
          {evidenceTypeLabel(evidence.type, t)}
        </div>
        <div className="text-muted mt-1 text-[11.5px]">
          {t("qerds.evidence.stampedAt", {
            when: dateFormatter.format(new Date(evidence.qualifiedTimestamp)),
          })}
        </div>
        <div className="text-ink-soft mt-0.5 font-mono text-[11px] break-all">
          {evidence.providerRef}
        </div>
        <details className="mt-1.5">
          <summary className="text-link cursor-pointer text-[11.5px]">
            {t("qerds.evidence.raw")}
          </summary>
          <pre className="bg-surface-2 border-line text-ink-soft mt-1.5 overflow-x-auto rounded-md border p-2.5 font-mono text-[11px]">
            {decodeEvidence(evidence.raw)}
          </pre>
        </details>
      </div>
    </li>
  );
}

function AttachmentsCard({
  slug,
  messageId,
  attachments,
  t,
}: {
  slug: string;
  messageId: string;
  attachments: QerdsAttachment[];
  t: TFunction;
}): React.JSX.Element {
  const [downloadingId, setDownloadingId] = React.useState<string | null>(null);

  async function download(attachment: QerdsAttachment): Promise<void> {
    setDownloadingId(attachment.id);
    try {
      await downloadQerdsAttachment(slug, messageId, attachment);
    } catch {
      toast.error(t("qerds.attachments.downloadError"));
    } finally {
      setDownloadingId(null);
    }
  }

  return (
    <Card className="p-6">
      <h2 className="text-[16px] font-semibold">
        {t("qerds.attachments.title")}
      </h2>
      <ul className="mt-4 flex flex-col gap-2">
        {attachments.map((attachment) => (
          <li
            key={attachment.id}
            className="border-line bg-surface-2 flex items-center gap-2.5 rounded-md border px-3 py-2"
          >
            <Icon name="lock" size={15} className="text-ink-soft shrink-0" />
            <span className="text-ink flex-1 truncate text-[13.5px]">
              {attachment.filename}
            </span>
            <span className="text-muted shrink-0 text-[11.5px]">
              {formatBytes(attachment.sizeBytes)}
            </span>
            <Button
              variant="secondary"
              size="sm"
              icon="arrow_front"
              loading={downloadingId === attachment.id}
              disabled={downloadingId !== null}
              onClick={() => void download(attachment)}
            >
              {t("qerds.attachments.download")}
            </Button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

export default function QerdsMessage(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { orgSlug, messageId } = useParams();
  // Both are guaranteed by the ":orgSlug/qerds/:messageId" route.
  const slug = orgSlug!;
  const id = messageId!;

  const org = useOrganizationQuery(slug);
  const messageQuery = useQerdsMessageQuery(slug, id, !org.isError);
  const message = messageQuery.data;
  const notFound =
    messageQuery.error instanceof ApiError && messageQuery.error.status === 404;

  const dateFormatter = React.useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: "medium",
        timeStyle: "medium",
        hour12: false,
      }),
    [i18n.language],
  );

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={t("qerds.message.title")} />
      <div className="p-8">{body}</div>
    </>
  );
  const notice = (text: string, isError = false): React.JSX.Element => (
    <Card className="p-6">
      <p className={`text-[14px] ${isError ? "text-error" : "text-ink-soft"}`}>
        {text}
      </p>
    </Card>
  );

  if (org.isError) {
    return shell(notice(accessMessage(org.error, t), true));
  }
  if (notFound) {
    return shell(notice(t("qerds.message.notFound")));
  }
  if (messageQuery.isError) {
    return shell(notice(accessMessage(messageQuery.error, t), true));
  }
  if (org.isPending || messageQuery.isPending) {
    return shell(notice(t("common.loading")));
  }
  if (!message) {
    return shell(notice(t("qerds.message.notFound")));
  }

  const isInbound = message.direction === "inbound";

  return (
    <>
      <TopBar
        title={message.subject}
        subtitle={
          isInbound
            ? t("qerds.message.fromLine", { address: message.senderAddress })
            : t("qerds.message.toLine", { address: message.recipientAddress })
        }
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-4">
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("qerds.message.content")}
            </h2>
            {message.body.trim() === "" ? (
              <p className="text-ink-soft mt-2 text-[14px] italic">
                {t("qerds.message.noBody")}
              </p>
            ) : (
              <p className="text-ink mt-2 text-[14px] whitespace-pre-wrap">
                {message.body}
              </p>
            )}
          </Card>

          {message.attachments.length > 0 && (
            <AttachmentsCard
              slug={slug}
              messageId={id}
              attachments={message.attachments}
              t={t}
            />
          )}

          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("qerds.evidence.title")}
            </h2>
            <p className="text-ink-soft mt-1 text-[13px]">
              {t("qerds.evidence.description")}
            </p>
            {message.evidence.length === 0 ? (
              <p className="text-ink-soft mt-4 text-[14px]">
                {t("qerds.evidence.empty")}
              </p>
            ) : (
              <ul className="mt-5">
                {message.evidence.map((evidence) => (
                  <EvidenceItem
                    key={evidence.id}
                    evidence={evidence}
                    dateFormatter={dateFormatter}
                    t={t}
                  />
                ))}
              </ul>
            )}
          </Card>
        </div>

        <Card className="h-fit p-0">
          <div className="border-line flex items-center gap-2.5 border-b p-5">
            <Tag tone={qerdsStatusTone(message.status)} dot>
              <span className="capitalize">{message.status}</span>
            </Tag>
            <Tag tone={isInbound ? "blue" : "default"}>
              {isInbound
                ? t("qerds.direction.inbound")
                : t("qerds.direction.outbound")}
            </Tag>
          </div>
          <div className="flex flex-col gap-2.5 p-5">
            <DetailRow
              label={t("qerds.columns.from")}
              value={message.senderAddress}
              mono
            />
            <DetailRow
              label={t("qerds.columns.to")}
              value={message.recipientAddress}
              mono
            />
            {message.providerRef && (
              <DetailRow
                label={t("qerds.message.providerRef")}
                value={message.providerRef}
                mono
              />
            )}
            {message.qualifiedTimestampSend && (
              <DetailRow
                label={t("qerds.message.sentAt")}
                value={dateFormatter.format(
                  new Date(message.qualifiedTimestampSend),
                )}
              />
            )}
            {message.deliveredAt && (
              <DetailRow
                label={t("qerds.message.deliveredAt")}
                value={dateFormatter.format(new Date(message.deliveredAt))}
              />
            )}
          </div>
        </Card>
      </div>
    </>
  );
}
