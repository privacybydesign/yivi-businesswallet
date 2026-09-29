import { useEffect, useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import QRCode from "qrcode";
import * as React from "react";
import type { TFunction } from "i18next";
import { useAttestationSchemasQuery } from "../api/attestations.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import type {
  Verification,
  VerificationCheck,
  VerificationStatus,
  VerificationTemplate,
} from "../api/verifications";
import {
  useDeleteVerificationTemplateMutation,
  useStartVerificationMutation,
  useVerificationQuery,
  useVerificationsQuery,
  useVerificationTemplatesQuery,
} from "../api/verifications.queries";
import { accessMessage } from "../lib/access-message";
import { control } from "../lib/attestation-form";
import { useWhenFormatter } from "../lib/format-when";
import { toast } from "../lib/toast";
import { statusTone, verdictTone } from "../lib/verification";
import { Button, Card, ConfirmDialog, Icon, Table, Tag, TopBar } from "../ui";
import { Field } from "./attestations-fields";
import { VerificationTemplateForm } from "./verification-template-form";

const ADMIN_ROLE = "admin";
const QR_SIZE = 240;
const HISTORY_COLUMN_COUNT = 5;

// statusLabel keeps the typed-i18n guarantee with a literal-key switch, like
// lib/audit-event.ts.
function statusLabel(t: TFunction, status: VerificationStatus): string {
  switch (status) {
    case "pending":
      return t("verifications.status.pending");
    case "completed":
      return t("verifications.status.completed");
    case "expired":
      return t("verifications.status.expired");
  }
}

// checkLabel names a backend check; an unknown one shows its raw name rather
// than hiding a row the backend thought worth reporting.
function checkLabel(t: TFunction, name: string): string {
  switch (name) {
    case "verified":
      return t("verifications.checks.verified");
    case "not_expired":
      return t("verifications.checks.notExpired");
    case "issued_here":
      return t("verifications.checks.issuedHere");
    case "not_revoked":
      return t("verifications.checks.notRevoked");
    default:
      return name;
  }
}

export default function Verifications(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === ADMIN_ROLE;
  const enabled = !org.isError;
  const templates = useVerificationTemplatesQuery(slug, enabled);
  const history = useVerificationsQuery(slug, enabled);
  const schemas = useAttestationSchemasQuery(slug, enabled && isAdmin);

  // The check shown in the active card: the one just started, or one picked
  // from the history.
  const [activeId, setActiveId] = useState<string | null>(null);
  const [templateModal, setTemplateModal] = useState(false);

  if (org.isError) {
    return (
      <>
        <TopBar title={t("verifications.title")} />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        </div>
      </>
    );
  }

  return (
    <>
      <TopBar
        title={t("verifications.title")}
        subtitle={t("verifications.subtitle")}
        actions={
          isAdmin ? (
            <Button
              variant="secondary"
              icon="add"
              onClick={() => setTemplateModal(true)}
            >
              {t("verifications.templates.new")}
            </Button>
          ) : undefined
        }
      />

      <div className="flex flex-col gap-6 p-4 sm:p-8">
        {templates.error ? (
          <ErrorCard
            message={t("verifications.loadError", {
              message: templates.error.message,
            })}
          />
        ) : activeId ? (
          <ActiveCheck
            slug={slug}
            id={activeId}
            onReset={() => setActiveId(null)}
          />
        ) : (
          <StartCard
            slug={slug}
            templates={templates.data ?? []}
            pending={templates.isPending}
            isAdmin={isAdmin}
            onStarted={(session) => setActiveId(session.id)}
          />
        )}

        <HistoryTable
          rows={history.data ?? []}
          pending={history.isPending}
          error={history.error}
          onView={(id) => setActiveId(id)}
        />

        {isAdmin && (
          <TemplatesSection
            slug={slug}
            templates={templates.data ?? []}
            pending={templates.isPending}
          />
        )}
      </div>

      {templateModal && (
        <VerificationTemplateForm
          slug={slug}
          schemas={schemas.data ?? []}
          onClose={() => setTemplateModal(false)}
        />
      )}
    </>
  );
}

function ErrorCard({ message }: { message: string }): React.JSX.Element {
  return (
    <Card className="p-6">
      <p className="text-error text-[14px]">{message}</p>
    </Card>
  );
}

function StartCard({
  slug,
  templates,
  pending,
  isAdmin,
  onStarted,
}: {
  slug: string;
  templates: VerificationTemplate[];
  pending: boolean;
  isAdmin: boolean;
  onStarted: (session: Verification) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const start = useStartVerificationMutation(slug);
  const [templateId, setTemplateId] = useState("");
  // The first template is the default pick, so a handhaver with one template
  // taps once.
  const selectedId = templateId !== "" ? templateId : (templates[0]?.id ?? "");
  const selected = templates.find((template) => template.id === selectedId);

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (selectedId === "" || start.isPending) {
      return;
    }
    start.mutate({ templateId: selectedId }, { onSuccess: onStarted });
  }

  return (
    <Card className="p-6">
      <h2 className="text-[16px] font-semibold">
        {t("verifications.start.title")}
      </h2>
      {pending ? (
        <p className="text-ink-soft mt-3 text-[14px]">{t("common.loading")}</p>
      ) : templates.length === 0 ? (
        <p className="text-ink-soft mt-3 text-[14px]">
          {isAdmin
            ? t("verifications.start.noTemplatesAdmin")
            : t("verifications.start.noTemplates")}
        </p>
      ) : (
        <form
          onSubmit={handleSubmit}
          className="mt-4 flex flex-col gap-4 sm:max-w-md"
        >
          <Field
            id="verification-template"
            label={t("verifications.start.template")}
          >
            <select
              id="verification-template"
              className={`${control(false)} h-9`}
              value={selectedId}
              onChange={(event) => setTemplateId(event.target.value)}
            >
              {templates.map((template) => (
                <option key={template.id} value={template.id}>
                  {template.name}
                </option>
              ))}
            </select>
          </Field>
          {selected && (
            <dl className="grid grid-cols-[96px_1fr] gap-y-1 text-[13px]">
              <dt className="text-muted">{t("verifications.start.claims")}</dt>
              <dd className="text-ink-soft font-mono text-[12px] break-words">
                {selected.claims.join(", ")}
              </dd>
              {selected.purpose !== "" && (
                <>
                  <dt className="text-muted">
                    {t("verifications.start.purpose")}
                  </dt>
                  <dd className="text-ink-soft">{selected.purpose}</dd>
                </>
              )}
            </dl>
          )}
          <div>
            <Button
              type="submit"
              size="lg"
              icon="scan_qrcode"
              loading={start.isPending}
            >
              {t("verifications.start.button")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}

function ActiveCheck({
  slug,
  id,
  onReset,
}: {
  slug: string;
  id: string;
  onReset: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const session = useVerificationQuery(slug, id);

  if (session.isError) {
    return (
      <ErrorCard
        message={t("verifications.loadError", {
          message: session.error.message,
        })}
      />
    );
  }
  if (session.isPending) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }

  const data = session.data;
  return (
    <Card className="p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-[16px] font-semibold">{data.templateName}</h2>
          <p className="text-ink-soft truncate font-mono text-[12px]">
            {data.vct}
          </p>
        </div>
        <Tag tone={statusTone(data.status, data.valid)} dot>
          {statusLabel(t, data.status)}
        </Tag>
      </div>

      {data.status === "pending" && <PendingRequest session={data} />}
      {data.status === "completed" && <ResultCard session={data} />}
      {data.status === "expired" && (
        <p className="text-ink-soft mt-4 text-[14px]">
          {t("verifications.session.expiredHint")}
        </p>
      )}

      <div className="mt-6">
        <Button variant="secondary" icon="arrow_back" onClick={onReset}>
          {t("verifications.session.newCheck")}
        </Button>
      </div>
    </Card>
  );
}

function PendingRequest({
  session,
}: {
  session: Verification;
}): React.JSX.Element {
  const { t } = useTranslation();
  const [qrDataUrl, setQrDataUrl] = useState("");
  const browserLink = session.browserLink ?? "";
  const walletLink = session.walletLink ?? "";

  // The QR carries the https form: a phone camera opens the holder's business
  // wallet in the browser, where the openid4vp:// scheme would be handed to a
  // personal wallet app instead.
  useEffect(() => {
    if (browserLink === "") {
      return;
    }
    let cancelled = false;
    void QRCode.toDataURL(browserLink, { margin: 1, width: QR_SIZE })
      .then((url) => {
        if (!cancelled) {
          setQrDataUrl(url);
        }
      })
      .catch(() => {
        // The copy buttons still work even if QR rendering fails.
      });
    return () => {
      cancelled = true;
    };
  }, [browserLink]);

  function copy(value: string): void {
    void navigator.clipboard
      .writeText(value)
      .then(() => toast.success(t("common.copied")))
      .catch(() => toast.error(t("common.copyFailed")));
  }

  return (
    <div className="mt-6 flex flex-col items-center gap-4">
      <div
        className="border-line-strong bg-surface rounded-yivi flex items-center justify-center border"
        style={{ width: QR_SIZE, height: QR_SIZE }}
      >
        {qrDataUrl ? (
          <img
            src={qrDataUrl}
            alt=""
            width={QR_SIZE}
            height={QR_SIZE}
            className="rounded-yivi"
          />
        ) : (
          <span
            aria-hidden="true"
            className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
          />
        )}
      </div>
      <p className="text-ink-soft max-w-sm text-center text-[13px]">
        {t("verifications.session.scanHint")}
      </p>
      <div className="flex flex-wrap justify-center gap-2">
        <Button
          variant="secondary"
          onClick={() => copy(browserLink)}
          disabled={browserLink === ""}
        >
          {t("verifications.session.copyLink")}
        </Button>
        <Button
          variant="ghost"
          onClick={() => copy(walletLink)}
          disabled={walletLink === ""}
        >
          {t("verifications.session.copyWalletLink")}
        </Button>
      </div>
      <p className="text-muted flex items-center gap-1.5 text-[12.5px]">
        <Icon name="time" size={13} />
        {t("verifications.session.waiting")}
      </p>
    </div>
  );
}

function ResultCard({ session }: { session: Verification }): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const checks = session.checks ?? [];
  const claims = Object.entries(session.claims ?? {});
  const tone = verdictTone(session);

  return (
    <div className="mt-4 flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <span
          className={[
            "inline-flex h-10 w-10 items-center justify-center rounded-full",
            tone === "green"
              ? "bg-success-bg text-success"
              : "bg-error-bg text-error",
          ].join(" ")}
        >
          <Icon name={tone === "green" ? "valid" : "invalid"} size={20} />
        </span>
        <div>
          <div className="text-ink text-[16px] font-semibold">
            {session.valid
              ? t("verifications.result.valid")
              : t("verifications.result.invalid")}
          </div>
          {session.completedAt && (
            <div className="text-ink-soft text-[12.5px]">
              {formatWhen(session.completedAt)}
            </div>
          )}
        </div>
      </div>

      <div>
        <h3 className="text-ink-soft text-[12px] font-semibold uppercase">
          {t("verifications.result.checks")}
        </h3>
        <ul className="mt-2 flex flex-col gap-1.5">
          {checks.map((check) => (
            <CheckRow key={check.name} check={check} />
          ))}
        </ul>
      </div>

      {claims.length > 0 && (
        <div>
          <h3 className="text-ink-soft text-[12px] font-semibold uppercase">
            {t("verifications.result.disclosed")}
          </h3>
          <dl className="mt-2 grid grid-cols-[minmax(120px,40%)_1fr] gap-y-1.5 text-[13.5px]">
            {claims.map(([key, value]) => (
              <React.Fragment key={key}>
                <dt className="text-muted font-mono text-[12px] break-words">
                  {key}
                </dt>
                <dd className="text-ink break-words">{value}</dd>
              </React.Fragment>
            ))}
          </dl>
        </div>
      )}
    </div>
  );
}

function CheckRow({ check }: { check: VerificationCheck }): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <li className="flex items-center justify-between gap-3 text-[13.5px]">
      <span className="text-ink">{checkLabel(t, check.name)}</span>
      <span className="flex items-center gap-2">
        {check.detail && (
          <span className="text-muted font-mono text-[11.5px]">
            {check.detail}
          </span>
        )}
        <Tag tone={check.passed ? "green" : "red"} dot>
          {check.passed
            ? t("verifications.result.passed")
            : t("verifications.result.failed")}
        </Tag>
      </span>
    </li>
  );
}

function HistoryTable({
  rows,
  pending,
  error,
  onView,
}: {
  rows: Verification[];
  pending: boolean;
  error: Error | null;
  onView: (id: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();

  if (error) {
    return (
      <ErrorCard
        message={t("verifications.loadError", { message: error.message })}
      />
    );
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-[16px] font-semibold">
        {t("verifications.history.title")}
      </h2>
      <Card className="overflow-hidden">
        <Table>
          <Table.Head>
            <Table.HeaderCell>
              {t("verifications.history.when")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("verifications.history.template")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("verifications.history.status")}
            </Table.HeaderCell>
            <Table.HeaderCell>
              {t("verifications.history.result")}
            </Table.HeaderCell>
            <Table.HeaderCell srOnly>
              {t("verifications.history.actions")}
            </Table.HeaderCell>
          </Table.Head>
          <Table.Body>
            {pending ? (
              <Table.State colSpan={HISTORY_COLUMN_COUNT}>
                {t("common.loading")}
              </Table.State>
            ) : rows.length === 0 ? (
              <Table.State colSpan={HISTORY_COLUMN_COUNT}>
                {t("verifications.history.empty")}
              </Table.State>
            ) : (
              rows.map((row) => (
                <Table.Row key={row.id}>
                  <Table.Cell className="text-ink-soft text-[12.5px] whitespace-nowrap">
                    {formatWhen(row.createdAt)}
                  </Table.Cell>
                  <Table.Cell className="text-ink">
                    {row.templateName}
                  </Table.Cell>
                  <Table.Cell>
                    <Tag tone={statusTone(row.status, row.valid)} dot>
                      {statusLabel(t, row.status)}
                    </Tag>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft text-[13px]">
                    {row.status === "completed"
                      ? row.valid
                        ? t("verifications.result.valid")
                        : t("verifications.result.invalid")
                      : "—"}
                  </Table.Cell>
                  <Table.Cell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => onView(row.id)}
                    >
                      {t("verifications.history.view")}
                    </Button>
                  </Table.Cell>
                </Table.Row>
              ))
            )}
          </Table.Body>
        </Table>
      </Card>
    </section>
  );
}

function TemplatesSection({
  slug,
  templates,
  pending,
}: {
  slug: string;
  templates: VerificationTemplate[];
  pending: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteVerificationTemplateMutation(slug);
  const [pendingDelete, setPendingDelete] =
    useState<VerificationTemplate | null>(null);

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-[16px] font-semibold">
        {t("verifications.templates.title")}
      </h2>
      {pending ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        </Card>
      ) : templates.length === 0 ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">
            {t("verifications.templates.empty")}
          </p>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {templates.map((template) => (
            <Card key={template.id} className="flex flex-col gap-3 p-4">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="text-ink truncate font-semibold">
                    {template.name}
                  </div>
                  <div className="text-ink-soft truncate font-mono text-[12px]">
                    {template.vct}
                  </div>
                </div>
                <Button
                  variant="dangerGhost"
                  size="sm"
                  icon="delete"
                  iconOnly
                  onClick={() => setPendingDelete(template)}
                  aria-label={t("verifications.templates.delete")}
                />
              </div>
              <div className="flex flex-wrap gap-1.5">
                {template.claims.map((claim) => (
                  <span
                    key={claim}
                    className="bg-surface-3 text-ink-soft rounded-full px-2 py-0.5 font-mono text-[11.5px] font-medium"
                  >
                    {claim}
                  </span>
                ))}
              </div>
              {template.purpose !== "" && (
                <p className="text-ink-soft text-[12.5px]">
                  {template.purpose}
                </p>
              )}
            </Card>
          ))}
        </div>
      )}
      {pendingDelete && (
        <ConfirmDialog
          title={t("verifications.templates.delete")}
          message={t("verifications.templates.confirmDelete", {
            name: pendingDelete.name,
          })}
          confirmLabel={t("verifications.templates.delete")}
          confirmVariant="danger"
          busy={remove.isPending}
          onConfirm={() => {
            remove.mutate({ templateId: pendingDelete.id });
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </section>
  );
}
