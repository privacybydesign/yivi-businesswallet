import { useTranslation } from "react-i18next";
import * as React from "react";
import { useSigningRequestsQuery } from "../api/signing.queries";
import {
  DELIVERY_STATUS,
  RECIPIENT_CHANNEL,
  SIGNER_STATUS,
  SIGNING_STATUS,
  downloadSignedDocument,
} from "../api/signing";
import type { SigningRequest } from "../api/signing";
import {
  channelLabel,
  deliveryStatusLabel,
  requestStatusLabel,
} from "../lib/signing-labels";
import { useWhenFormatter } from "../lib/format-when";
import { toast } from "../lib/toast";
import { Button, Card, Table, Tag } from "../ui";

const COLUMN_COUNT = 6;

// SigningHistoryPanel is the admin-only "History" tab of the signing page: the
// org's signing requests newest-first, cursor-paginated. It renders no TopBar — the
// signing page owns that; the tab is only mounted for admins (enabled below).
export function SigningHistoryPanel({
  slug,
  enabled,
}: {
  slug: string;
  enabled: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const history = useSigningRequestsQuery(slug, enabled);
  const requests = history.data?.pages.flatMap((p) => p.requests ?? []) ?? [];
  const formatWhen = useWhenFormatter();

  const onDownload = (req: SigningRequest): void => {
    void downloadSignedDocument(slug, req.id, req.filename)
      .then(() => toast.success(t("signing.downloadedToast")))
      .catch(() => toast.error(t("signing.downloadError")));
  };

  if (history.isError) {
    return (
      <Card className="p-6">
        <p className="text-error text-[14px]">
          {t("signedDocuments.loadError", { message: history.error.message })}
        </p>
      </Card>
    );
  }

  return (
    <>
      <Card className="overflow-hidden">
        <Table>
          <Table.Head>
            <Table.HeaderCell>
              {t("signedDocuments.columns.created")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("signedDocuments.columns.document")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("signedDocuments.columns.signers")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("signedDocuments.columns.recipient")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("signedDocuments.columns.status")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("signedDocuments.columns.document")}
            </Table.HeaderCell>
          </Table.Head>
          <Table.Body>
            {history.isPending ? (
              <Table.State colSpan={COLUMN_COUNT}>
                {t("common.loading")}
              </Table.State>
            ) : requests.length === 0 ? (
              <Table.State colSpan={COLUMN_COUNT}>
                {t("signedDocuments.empty")}
              </Table.State>
            ) : (
              requests.map((req) => (
                <Table.Row key={req.id}>
                  <Table.Cell>
                    <span className="text-ink-soft text-[12.5px]">
                      {formatWhen(req.createdAt)}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className="text-ink block max-w-[16rem] truncate">
                      {req.filename}
                    </span>
                    <span className="text-ink-soft text-[12px]">
                      {t("signing.requestedBy", { name: req.createdByName })}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <div className="flex flex-wrap gap-1.5">
                      {(req.signers ?? []).map((s) => (
                        <Tag
                          key={s.userId}
                          tone={
                            s.status === SIGNER_STATUS.signed
                              ? "green"
                              : s.status === SIGNER_STATUS.failed
                                ? "red"
                                : "default"
                          }
                        >
                          {s.name || s.email}
                        </Tag>
                      ))}
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    <RecipientCell req={req} />
                  </Table.Cell>
                  <Table.Cell>
                    <StatusCell req={req} />
                  </Table.Cell>
                  <Table.Cell>
                    {req.status === SIGNING_STATUS.completed ? (
                      <Button
                        variant="secondary"
                        onClick={() => onDownload(req)}
                      >
                        {t("signedDocuments.download")}
                      </Button>
                    ) : (
                      <span className="text-ink-soft text-[12.5px]">—</span>
                    )}
                  </Table.Cell>
                </Table.Row>
              ))
            )}
          </Table.Body>
        </Table>
      </Card>

      {history.hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button
            variant="secondary"
            onClick={() => void history.fetchNextPage()}
            disabled={history.isFetchingNextPage}
          >
            {history.isFetchingNextPage
              ? t("common.loading")
              : t("signedDocuments.loadMore")}
          </Button>
        </div>
      )}
    </>
  );
}

function RecipientCell({ req }: { req: SigningRequest }): React.JSX.Element {
  const { t } = useTranslation();
  if (req.recipientChannel === RECIPIENT_CHANNEL.none) {
    return <span className="text-ink-soft text-[12.5px]">—</span>;
  }
  return (
    <div className="text-[12.5px]">
      <span className="text-ink">
        {req.recipientName || req.recipientAddress}
      </span>
      <span className="text-ink-soft block">
        {channelLabel(t, req.recipientChannel)}
      </span>
    </div>
  );
}

function StatusCell({ req }: { req: SigningRequest }): React.JSX.Element {
  const { t } = useTranslation();
  const statusTone =
    req.status === SIGNING_STATUS.completed
      ? "green"
      : req.status === SIGNING_STATUS.failed
        ? "red"
        : "default";
  return (
    <div className="flex flex-col gap-1">
      <Tag tone={statusTone}>{requestStatusLabel(t, req.status)}</Tag>
      {req.deliveryStatus !== DELIVERY_STATUS.notRequested && (
        <Tag
          tone={
            req.deliveryStatus === DELIVERY_STATUS.delivered
              ? "green"
              : req.deliveryStatus === DELIVERY_STATUS.failed
                ? "red"
                : "default"
          }
        >
          {deliveryStatusLabel(t, req.deliveryStatus)}
        </Tag>
      )}
    </div>
  );
}
