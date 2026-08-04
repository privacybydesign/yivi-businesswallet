import { useNavigate, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  usePollQerdsInboxMutation,
  useQerdsMessagesQuery,
} from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { useWhenFormatter } from "../lib/format-when";
import { qerdsStatusTone } from "../lib/qerds";
import {
  loadSeenIds,
  pruneSeen,
  saveSeenIds,
  unreadInboundIds,
} from "../lib/qerds-unread";
import { Button, Card, Icon, Table, Tag, TopBar } from "../ui";
import * as React from "react";

const COLUMN_COUNT = 4;

type Box = "inbox" | "outbox";

const BOXES: readonly Box[] = ["inbox", "outbox"];

function readBox(params: URLSearchParams): Box {
  return params.get("box") === "outbox" ? "outbox" : "inbox";
}

export default function Qerds(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const [searchParams, setSearchParams] = useSearchParams();
  const box = readBox(searchParams);

  const messages = useQerdsMessagesQuery(slug, !org.isError);
  const poll = usePollQerdsInboxMutation(slug);
  const formatWhen = useWhenFormatter();

  const all = messages.data ?? [];
  const rows = all.filter((message) =>
    box === "inbox"
      ? message.direction === "inbound"
      : message.direction === "outbound",
  );

  const inboundIds = React.useMemo(
    () =>
      (messages.data ?? [])
        .filter((message) => message.direction === "inbound")
        .map((message) => message.id),
    [messages.data],
  );

  // Which inbound messages this browser has already seen. null until the first
  // load resolves, so we can baseline a first visit (everything already seen)
  // instead of flashing the whole inbox as new.
  const [seen, setSeen] = React.useState<string[] | null>(() =>
    loadSeenIds(slug),
  );

  // Baseline and prune the seen set whenever the inbox contents change, using
  // the "adjust state during render" pattern rather than an effect: a first
  // visit baselines the whole inbox as already seen, and later fetches drop ids
  // that have left the inbox so the stored set stays bounded by the inbox size.
  const inboundKey = inboundIds.join(",");
  const [trackedKey, setTrackedKey] = React.useState<string | null>(null);
  if (messages.data !== undefined && trackedKey !== inboundKey) {
    setTrackedKey(inboundKey);
    setSeen((prev) => pruneSeen(prev ?? inboundIds, inboundIds));
  }

  // Persist the seen set (side effect only, never a state update).
  React.useEffect(() => {
    if (seen !== null) saveSeenIds(slug, seen);
  }, [slug, seen]);

  const unreadIds = React.useMemo(
    () => new Set(unreadInboundIds(inboundIds, seen ?? inboundIds)),
    [inboundIds, seen],
  );

  const markSeen = (id: string): void => {
    setSeen((prev) => {
      const current = prev ?? inboundIds;
      if (current.includes(id)) return current;
      return [...current, id];
    });
  };

  const setBox = (value: Box): void => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === "inbox") next.delete("box");
      else next.set("box", value);
      return next;
    });
  };

  return (
    <>
      <TopBar
        title={t("qerds.title")}
        subtitle={t("qerds.subtitle")}
        actions={
          <>
            <Button
              variant="secondary"
              icon="personal"
              onClick={() => void navigate(`/${slug}/qerds/contacts`)}
            >
              {t("qerds.contacts.title")}
            </Button>
            <Button
              variant="secondary"
              icon="lock"
              onClick={() => void navigate(`/${slug}/qerds/addresses`)}
            >
              {t("qerds.addresses.title")}
            </Button>
            <Button
              variant="secondary"
              icon="time"
              onClick={() => poll.mutate()}
              disabled={poll.isPending}
            >
              {poll.isPending ? t("qerds.checking") : t("qerds.checkInbox")}
            </Button>
            <Button
              icon="add"
              onClick={() => void navigate(`/${slug}/qerds/compose`)}
            >
              {t("qerds.newMessage")}
            </Button>
          </>
        }
      />

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : messages.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("qerds.loadError", { message: messages.error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <div className="border-line flex items-center gap-3 border-b px-4 py-3">
              <div className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]">
                {BOXES.map((value) => (
                  <button
                    key={value}
                    type="button"
                    onClick={() => setBox(value)}
                    className={[
                      "inline-flex h-[26px] cursor-pointer items-center gap-1.5 rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
                      box === value
                        ? "bg-surface text-ink shadow-sm"
                        : "text-ink-soft hover:text-ink",
                    ].join(" ")}
                  >
                    {value === "inbox"
                      ? t("qerds.tabs.inbox")
                      : t("qerds.tabs.outbox")}
                    {value === "inbox" && unreadIds.size > 0 && (
                      <span
                        className="bg-highlight text-link inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full px-1 text-[11px] font-semibold"
                        aria-label={t("qerds.unread", {
                          count: unreadIds.size,
                        })}
                      >
                        {unreadIds.size}
                      </span>
                    )}
                  </button>
                ))}
              </div>
            </div>

            <Table className="table-fixed">
              <Table.Head>
                <Table.HeaderCell className="w-[40%]">
                  {t("qerds.columns.subject")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[28%]">
                  {box === "inbox"
                    ? t("qerds.columns.from")
                    : t("qerds.columns.to")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[16%]">
                  {t("qerds.columns.status")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[16%]">
                  {t("qerds.columns.when")}
                </Table.HeaderCell>
              </Table.Head>
              <Table.Body>
                {org.isPending || messages.isPending ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {t("common.loading")}
                  </Table.State>
                ) : rows.length === 0 ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {box === "inbox"
                      ? t("qerds.emptyInbox")
                      : t("qerds.emptyOutbox")}
                  </Table.State>
                ) : (
                  rows.map((message) => {
                    const unread = box === "inbox" && unreadIds.has(message.id);
                    return (
                      <Table.Row
                        key={message.id}
                        onClick={() => {
                          markSeen(message.id);
                          void navigate(`/${slug}/qerds/${message.id}`);
                        }}
                        className="hover:bg-surface-3 cursor-pointer transition-colors"
                      >
                        <Table.Cell>
                          <div className="flex items-center gap-2.5">
                            {unread ? (
                              <span className="bg-link h-2 w-2 shrink-0 rounded-full">
                                <span className="sr-only">
                                  {t("qerds.unreadItem")}
                                </span>
                              </span>
                            ) : (
                              <Icon
                                name="email"
                                size={15}
                                className="text-ink-soft shrink-0"
                              />
                            )}
                            <span
                              className={[
                                "block truncate",
                                unread ? "text-ink font-semibold" : "text-ink",
                              ].join(" ")}
                            >
                              {message.subject}
                            </span>
                          </div>
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft truncate font-mono text-[12.5px]">
                          {box === "inbox"
                            ? message.senderAddress
                            : message.recipientAddress}
                        </Table.Cell>
                        <Table.Cell>
                          <Tag tone={qerdsStatusTone(message.status)} dot>
                            <span className="capitalize">{message.status}</span>
                          </Tag>
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft text-[12.5px]">
                          {formatWhen(message.createdAt)}
                        </Table.Cell>
                      </Table.Row>
                    );
                  })
                )}
              </Table.Body>
            </Table>
          </Card>
        )}
      </div>
    </>
  );
}
