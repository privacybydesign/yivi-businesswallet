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
                      "h-[26px] cursor-pointer rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
                      box === value
                        ? "bg-surface text-ink shadow-sm"
                        : "text-ink-soft hover:text-ink",
                    ].join(" ")}
                  >
                    {value === "inbox"
                      ? t("qerds.tabs.inbox")
                      : t("qerds.tabs.outbox")}
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
                  rows.map((message) => (
                    <Table.Row
                      key={message.id}
                      onClick={() =>
                        void navigate(`/${slug}/qerds/${message.id}`)
                      }
                      className="hover:bg-surface-3 cursor-pointer transition-colors"
                    >
                      <Table.Cell>
                        <div className="flex items-center gap-2.5">
                          <Icon
                            name="email"
                            size={15}
                            className="text-ink-soft shrink-0"
                          />
                          <span className="text-ink block truncate">
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
                  ))
                )}
              </Table.Body>
            </Table>
          </Card>
        )}
      </div>
    </>
  );
}
