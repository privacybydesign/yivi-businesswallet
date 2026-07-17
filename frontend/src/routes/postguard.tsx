import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  usePostguardFilesQuery,
  usePostguardSettingsQuery,
} from "../api/postguard.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { postguardReadiness } from "../lib/postguard-availability";
import { useWhenFormatter } from "../lib/format-when";
import { formatBytes } from "../lib/format-bytes";
import { Button, Card, Icon, Table, Tag, TopBar } from "../ui";
import { PostguardNotReady } from "./postguard-not-ready";
import * as React from "react";

const COLUMN_COUNT = 4;

function recipientLabel(recipients: string[]): string {
  if (recipients.length === 0) {
    return "";
  }
  const [first, ...rest] = recipients;
  return rest.length > 0 ? `${first} +${rest.length}` : first;
}

export default function Postguard(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const files = usePostguardFilesQuery(slug, !org.isError);
  const settings = usePostguardSettingsQuery(slug, !org.isError);
  const formatWhen = useWhenFormatter();

  const isAdmin = org.data?.role === "admin";
  const rows = files.data ?? [];

  const readiness = postguardReadiness(
    settings.data,
    settings.error,
    settings.isPending,
  );
  const notReadyReason =
    readiness === "unconfigured" || readiness === "deploymentOff"
      ? readiness
      : null;

  return (
    <>
      <TopBar
        title={t("postguard.title")}
        subtitle={t("postguard.subtitle")}
        actions={
          <Button
            icon="lock"
            disabled={readiness !== "ready"}
            onClick={() => void navigate(`/${slug}/postguard/send`)}
          >
            {t("postguard.send.action")}
          </Button>
        }
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : notReadyReason ? (
          <PostguardNotReady reason={notReadyReason} isAdmin={isAdmin} />
        ) : files.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("postguard.loadError", { message: files.error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <Table className="table-fixed">
              <Table.Head>
                <Table.HeaderCell className="w-[38%]">
                  {t("postguard.columns.file")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[30%]">
                  {t("postguard.columns.recipient")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[16%]">
                  {t("postguard.columns.sent")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[16%]">
                  {t("postguard.columns.state")}
                </Table.HeaderCell>
              </Table.Head>
              <Table.Body>
                {org.isPending || files.isPending ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {t("common.loading")}
                  </Table.State>
                ) : rows.length === 0 ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {t("postguard.empty")}
                  </Table.State>
                ) : (
                  rows.map((file) => (
                    <Table.Row key={file.id}>
                      <Table.Cell>
                        <div className="flex items-center gap-2.5">
                          <span className="bg-surface-3 flex h-8 w-8 shrink-0 items-center justify-center rounded-md">
                            <Icon
                              name="lock"
                              size={14}
                              className="text-ink-soft"
                            />
                          </span>
                          <div className="min-w-0">
                            <div className="text-ink truncate font-semibold">
                              {file.fileName}
                            </div>
                            <div className="text-ink-soft text-[12.5px]">
                              {t("postguard.fileMeta", {
                                size: formatBytes(file.sizeBytes),
                              })}
                            </div>
                          </div>
                        </div>
                      </Table.Cell>
                      <Table.Cell className="text-ink-soft truncate font-mono text-[12.5px]">
                        {recipientLabel(file.recipients)}
                      </Table.Cell>
                      <Table.Cell className="text-ink-soft text-[12.5px]">
                        {formatWhen(file.createdAt)}
                      </Table.Cell>
                      <Table.Cell>
                        <Tag tone="green" dot>
                          {t("postguard.state.sent")}
                        </Tag>
                      </Table.Cell>
                    </Table.Row>
                  ))
                )}
              </Table.Body>
            </Table>
          </Card>
        )}

        <div>
          <Card className="p-5">
            <div className="mb-2.5 flex items-center gap-2.5">
              <span className="bg-brand/10 text-brand-600 flex h-8.5 w-8.5 items-center justify-center rounded-lg">
                <Icon name="lock" size={17} />
              </span>
              <div>
                <div className="font-display font-bold">
                  {t("postguard.about.title")}
                </div>
                <div className="text-ink-soft text-[12.5px]">
                  {t("postguard.about.tagline")}
                </div>
              </div>
            </div>
            <p className="text-ink-soft text-[13px] leading-relaxed">
              {t("postguard.about.body")}
            </p>
          </Card>
        </div>
      </div>
    </>
  );
}
