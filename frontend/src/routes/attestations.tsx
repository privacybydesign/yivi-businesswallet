import { useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationSchema,
  AttestationTemplate,
  HeldAttestation,
  IssuedAttestation,
} from "../api/attestations";
import {
  useAttestationKeysQuery,
  useAttestationSchemasQuery,
  useAttestationTemplatesQuery,
  useDeleteAttestationSchemaMutation,
  useDeleteAttestationTemplateMutation,
  useDeleteHeldAttestationMutation,
  useHeldAttestationClaimsQuery,
  useHeldAttestationsQuery,
  useIssuedAttestationsQuery,
  useRevokeIssuedAttestationMutation,
} from "../api/attestations.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { credentialDisplayName } from "../lib/credential-display";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card, ConfirmDialog, Modal, Table, Tag, TopBar } from "../ui";
import { AttestationIssueWizard } from "./attestations-issue";
import { AttestationSchemaForm } from "./attestations-schema-form";
import { AttestationTemplateForm } from "./attestations-template-form";
import { WscaActivationNotice } from "./wsca-activation-notice";

const ISSUED_COLUMN_COUNT = 5;
const CHIP_LIMIT = 3;
const ADMIN_ROLE = "admin";

type IssuedTone = "default" | "green" | "amber" | "red" | "blue";

function issuedTone(status: string): IssuedTone {
  switch (status) {
    case "claimed":
      return "green";
    case "offered":
      return "amber";
    case "revoked":
    case "failed":
      return "red";
    case "expired":
      return "default";
    default:
      return "default";
  }
}

type Tab = "held" | "templates" | "issued" | "schemas";

const ADMIN_TABS: readonly Tab[] = ["held", "templates", "issued", "schemas"];
const MEMBER_TABS: readonly Tab[] = ["held", "issued"];

function readTab(params: URLSearchParams, tabs: readonly Tab[]): Tab {
  const value = params.get("tab");
  return tabs.find((tab) => tab === value) ?? tabs[0];
}

// The modal currently open, if any.
type ActiveModal =
  | { kind: "issue"; template?: AttestationTemplate }
  | { kind: "schema"; schema?: AttestationSchema }
  | { kind: "template"; template?: AttestationTemplate }
  | null;

export default function Attestations(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === ADMIN_ROLE;
  const tabs = isAdmin ? ADMIN_TABS : MEMBER_TABS;

  const [searchParams, setSearchParams] = useSearchParams();
  const tab = readTab(searchParams, tabs);
  const [modal, setModal] = useState<ActiveModal>(null);
  // The held credential whose attributes are being viewed, if any.
  const [selectedHeld, setSelectedHeld] = useState<HeldAttestation | null>(
    null,
  );

  const enabled = !org.isError;
  const issued = useIssuedAttestationsQuery(slug, enabled);
  const held = useHeldAttestationsQuery(slug, enabled);
  const templates = useAttestationTemplatesQuery(slug, enabled && isAdmin);
  const schemas = useAttestationSchemasQuery(slug, enabled && isAdmin);
  const keys = useAttestationKeysQuery(slug, enabled && isAdmin);

  const formatWhen = useWhenFormatter();

  // A template carries its schema's fields but not the credential image, so map
  // each schema id to its logo URL (absolute, "" when none) for the templates tab.
  const schemaLogos = useMemo(() => {
    const map = new Map<string, string>();
    for (const schema of schemas.data ?? []) {
      if (schema.logoUri) map.set(schema.id, schema.logoUri);
    }
    return map;
  }, [schemas.data]);

  const setTab = (value: Tab): void => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value === tabs[0]) next.delete("tab");
      else next.set("tab", value);
      return next;
    });
  };

  return (
    <>
      <TopBar
        title={t("attestations.title")}
        subtitle={t("attestations.subtitle")}
        actions={
          isAdmin ? (
            <>
              <Button
                variant="secondary"
                icon="add"
                onClick={() => setModal({ kind: "template" })}
              >
                {t("attestations.newTemplate")}
              </Button>
              <Button icon="valid" onClick={() => setModal({ kind: "issue" })}>
                {t("attestations.issue")}
              </Button>
            </>
          ) : undefined
        }
      />

      <div className="border-line bg-surface flex gap-1 border-b px-8">
        {tabs.map((value) => {
          const active = tab === value;
          return (
            <button
              key={value}
              type="button"
              onClick={() => setTab(value)}
              className={[
                "h-11 border-b-2 px-3.5 text-[13.5px] transition-colors",
                active
                  ? "border-primary text-ink font-semibold"
                  : "text-ink-soft hover:text-ink border-transparent font-medium",
              ].join(" ")}
            >
              {t(TAB_LABEL_KEYS[value])}
            </button>
          );
        })}
      </div>

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : (
          <div className="flex flex-col gap-5">
            <WscaActivationNotice slug={slug} isAdmin={isAdmin} />

            {tab === "templates" && (
              <TemplatesTab
                slug={slug}
                templates={templates.data ?? []}
                schemaLogos={schemaLogos}
                pending={templates.isPending}
                error={templates.error}
                onIssue={(template) => setModal({ kind: "issue", template })}
                onEdit={(template) => setModal({ kind: "template", template })}
              />
            )}

            {tab === "issued" && (
              <IssuedTab
                slug={slug}
                rows={issued.data ?? []}
                pending={issued.isPending}
                error={issued.error}
                isAdmin={isAdmin}
                formatWhen={formatWhen}
              />
            )}

            {tab === "held" && (
              <HeldTab
                slug={slug}
                rows={held.data ?? []}
                pending={held.isPending}
                error={held.error}
                isAdmin={isAdmin}
                formatWhen={formatWhen}
                onSelect={setSelectedHeld}
              />
            )}

            {tab === "schemas" && (
              <SchemasTab
                slug={slug}
                schemas={schemas.data ?? []}
                pending={schemas.isPending}
                error={schemas.error}
                onCreate={() => setModal({ kind: "schema" })}
                onEdit={(schema) => setModal({ kind: "schema", schema })}
              />
            )}
          </div>
        )}
      </div>

      {modal?.kind === "issue" && (
        <AttestationIssueWizard
          slug={slug}
          templates={templates.data ?? []}
          initialTemplate={modal.template}
          onClose={() => setModal(null)}
        />
      )}
      {modal?.kind === "schema" && (
        <AttestationSchemaForm
          slug={slug}
          schema={modal.schema}
          onClose={() => setModal(null)}
        />
      )}
      {modal?.kind === "template" && (
        <AttestationTemplateForm
          slug={slug}
          template={modal.template}
          schemas={schemas.data ?? []}
          keys={keys.data ?? []}
          onClose={() => setModal(null)}
        />
      )}
      {selectedHeld && (
        <HeldDetailModal
          slug={slug}
          held={selectedHeld}
          formatWhen={formatWhen}
          onClose={() => setSelectedHeld(null)}
        />
      )}
    </>
  );
}

const TAB_LABEL_KEYS = {
  held: "attestations.tabs.held",
  templates: "attestations.tabs.templates",
  issued: "attestations.tabs.issued",
  schemas: "attestations.tabs.schemas",
} as const;

function ErrorCard({ message }: { message: string }): React.JSX.Element {
  return (
    <Card className="p-6">
      <p className="text-error text-[14px]">{message}</p>
    </Card>
  );
}

function TemplatesTab({
  slug,
  templates,
  schemaLogos,
  pending,
  error,
  onIssue,
  onEdit,
}: {
  slug: string;
  templates: AttestationTemplate[];
  schemaLogos: Map<string, string>;
  pending: boolean;
  error: Error | null;
  onIssue: (template: AttestationTemplate) => void;
  onEdit: (template: AttestationTemplate) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteAttestationTemplateMutation(slug);
  const [pendingDelete, setPendingDelete] =
    useState<AttestationTemplate | null>(null);

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.loadError", { message: error.message })}
      />
    );
  }
  if (pending) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  if (templates.length === 0) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">
          {t("attestations.templates.empty")}
        </p>
      </Card>
    );
  }

  return (
    <>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {templates.map((template) => {
          const chips = template.attributes.slice(0, CHIP_LIMIT);
          const extra = template.attributes.length - chips.length;
          const logoUri = schemaLogos.get(template.schemaId);
          return (
            <Card key={template.id} className="flex flex-col gap-3 p-4">
              <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 items-center gap-3">
                  {logoUri && (
                    <img
                      src={logoUri}
                      alt={t("attestations.credentialImageAlt")}
                      className="border-line bg-surface h-10 w-10 shrink-0 rounded-md border object-contain"
                    />
                  )}
                  <div className="min-w-0">
                    <div className="text-ink truncate font-semibold">
                      {template.name}
                    </div>
                    <div className="text-ink-soft truncate font-mono text-[12px]">
                      {template.vct}
                    </div>
                  </div>
                </div>
                {template.qualified && (
                  <Tag tone="blue">{t("attestations.qualified")}</Tag>
                )}
              </div>

              <div className="flex flex-wrap gap-1.5">
                {chips.map((attribute) => (
                  <span
                    key={attribute.key}
                    className="bg-surface-3 text-ink-soft rounded-full px-2 py-0.5 text-[11.5px] font-medium"
                  >
                    {attribute.label || attribute.key}
                  </span>
                ))}
                {extra > 0 && (
                  <span className="text-ink-soft px-1 py-0.5 text-[11.5px] font-medium">
                    {t("attestations.templates.moreAttributes", {
                      count: extra,
                    })}
                  </span>
                )}
              </div>

              <div className="mt-auto flex items-center justify-between pt-1">
                <span className="text-ink-soft text-[12.5px]">
                  {t("attestations.templates.issuedCount", {
                    count: template.issuedCount,
                  })}
                </span>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="sm"
                    icon="edit"
                    iconOnly
                    onClick={() => onEdit(template)}
                    aria-label={t("common.edit")}
                  />
                  <Button
                    variant="dangerGhost"
                    size="sm"
                    icon="delete"
                    iconOnly
                    onClick={() => setPendingDelete(template)}
                    aria-label={t("attestations.templates.delete")}
                  />
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => onIssue(template)}
                  >
                    {t("attestations.templates.issueAction")}
                  </Button>
                </div>
              </div>
            </Card>
          );
        })}
      </div>
      {pendingDelete && (
        <ConfirmDialog
          title={t("attestations.templates.delete")}
          message={t("attestations.templates.confirmDelete", {
            name: pendingDelete.name,
          })}
          confirmLabel={t("attestations.templates.delete")}
          busy={remove.isPending}
          onConfirm={() => {
            remove.mutate({ templateId: pendingDelete.id });
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </>
  );
}

function IssuedTab({
  slug,
  rows,
  pending,
  error,
  isAdmin,
  formatWhen,
}: {
  slug: string;
  rows: IssuedAttestation[];
  pending: boolean;
  error: Error | null;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const revoke = useRevokeIssuedAttestationMutation(slug);
  const columnCount = isAdmin ? ISSUED_COLUMN_COUNT : ISSUED_COLUMN_COUNT - 1;

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.loadError", { message: error.message })}
      />
    );
  }

  return (
    <Card className="overflow-hidden">
      <Table className="table-fixed">
        <Table.Head>
          <Table.HeaderCell className="w-[28%]">
            {t("attestations.issued.columns.recipient")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[28%]">
            {t("attestations.issued.columns.schema")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.issued.columns.status")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.issued.columns.issued")}
          </Table.HeaderCell>
          {isAdmin && (
            <Table.HeaderCell className="w-[12%]" srOnly>
              {t("attestations.issued.columns.actions")}
            </Table.HeaderCell>
          )}
        </Table.Head>
        <Table.Body>
          {pending ? (
            <Table.State colSpan={columnCount}>
              {t("common.loading")}
            </Table.State>
          ) : rows.length === 0 ? (
            <Table.State colSpan={columnCount}>
              {t("attestations.issued.empty")}
            </Table.State>
          ) : (
            rows.map((row) => (
              <Table.Row key={row.id}>
                <Table.Cell className="text-ink truncate font-mono text-[12.5px]">
                  {row.recipientRef}
                </Table.Cell>
                <Table.Cell className="text-ink-soft truncate font-mono text-[12.5px]">
                  {row.schemaVct}
                </Table.Cell>
                <Table.Cell>
                  <Tag tone={issuedTone(row.status)} dot>
                    <span className="capitalize">{row.status}</span>
                  </Tag>
                </Table.Cell>
                <Table.Cell className="text-ink-soft text-[12.5px]">
                  {formatWhen(row.createdAt)}
                </Table.Cell>
                {isAdmin && (
                  <Table.Cell className="text-right">
                    {(row.status === "offered" || row.status === "claimed") && (
                      <Button
                        variant="dangerGhost"
                        size="sm"
                        onClick={() => revoke.mutate({ issuedId: row.id })}
                      >
                        {t("attestations.issued.revoke")}
                      </Button>
                    )}
                  </Table.Cell>
                )}
              </Table.Row>
            ))
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}

const HELD_COLUMN_COUNT = 5;

function HeldTab({
  slug,
  rows,
  pending,
  error,
  isAdmin,
  formatWhen,
  onSelect,
}: {
  slug: string;
  rows: HeldAttestation[];
  pending: boolean;
  error: Error | null;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
  onSelect: (row: HeldAttestation) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteHeldAttestationMutation(slug);
  const [pendingDelete, setPendingDelete] = useState<HeldAttestation | null>(
    null,
  );
  const columnCount = isAdmin ? HELD_COLUMN_COUNT : HELD_COLUMN_COUNT - 1;

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.loadError", { message: error.message })}
      />
    );
  }

  return (
    <Card className="overflow-hidden">
      <Table className="table-fixed">
        <Table.Head>
          <Table.HeaderCell className="w-[32%]">
            {t("attestations.held.columns.credential")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[24%]">
            {t("attestations.held.columns.issuer")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.held.columns.source")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.held.columns.received")}
          </Table.HeaderCell>
          {isAdmin && (
            <Table.HeaderCell className="w-[12%]" srOnly>
              {t("attestations.held.columns.actions")}
            </Table.HeaderCell>
          )}
        </Table.Head>
        <Table.Body>
          {pending ? (
            <Table.State colSpan={columnCount}>
              {t("common.loading")}
            </Table.State>
          ) : rows.length === 0 ? (
            <Table.State colSpan={columnCount}>
              {t("attestations.held.empty")}
            </Table.State>
          ) : (
            rows.map((row) => {
              const name = credentialDisplayName(row.vct);
              return (
                <Table.Row
                  key={row.id}
                  onClick={() => onSelect(row)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      onSelect(row);
                    }
                  }}
                  tabIndex={0}
                  role="button"
                  aria-label={t("attestations.held.viewDetail", { name })}
                  className="hover:bg-surface-2 focus-visible:bg-surface-2 cursor-pointer outline-none"
                >
                  <Table.Cell className="min-w-0">
                    <div className="text-ink truncate font-semibold">
                      {name}
                    </div>
                    <div className="text-ink-soft truncate font-mono text-[12px]">
                      {row.vct}
                    </div>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft truncate">
                    {row.issuer}
                  </Table.Cell>
                  <Table.Cell>
                    <Tag tone="default">
                      <span className="capitalize">{row.source}</span>
                    </Tag>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft text-[12.5px]">
                    {formatWhen(row.receivedAt)}
                  </Table.Cell>
                  {isAdmin && (
                    <Table.Cell className="text-right">
                      <Button
                        variant="dangerGhost"
                        size="sm"
                        onClick={(event) => {
                          event.stopPropagation();
                          setPendingDelete(row);
                        }}
                      >
                        {t("attestations.held.delete")}
                      </Button>
                    </Table.Cell>
                  )}
                </Table.Row>
              );
            })
          )}
        </Table.Body>
      </Table>
      {pendingDelete && (
        <ConfirmDialog
          title={t("attestations.held.delete")}
          message={t("attestations.held.confirmDelete", {
            name: credentialDisplayName(pendingDelete.vct),
          })}
          confirmLabel={t("attestations.held.delete")}
          busy={remove.isPending}
          onConfirm={() => {
            remove.mutate({ heldId: pendingDelete.id });
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </Card>
  );
}

// HeldDetailModal shows a held credential's friendly name, provenance metadata and
// its disclosed attributes (fetched on open from the holder engine). Attribute
// values are rendered generically since the SD-JWT payload may carry any JSON type.
function HeldDetailModal({
  slug,
  held,
  formatWhen,
  onClose,
}: {
  slug: string;
  held: HeldAttestation;
  formatWhen: (iso: string) => string;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const claims = useHeldAttestationClaimsQuery(slug, held.id);
  const name = credentialDisplayName(held.vct);
  const attributes = claims.data?.attributes ?? [];

  return (
    <Modal title={name} closeLabel={t("common.close")} onClose={onClose}>
      <div className="flex flex-col gap-5">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-[13px]">
          <dt className="text-ink-soft">
            {t("attestations.held.columns.issuer")}
          </dt>
          <dd className="text-ink">{claims.data?.issuerName || held.issuer}</dd>
          <dt className="text-ink-soft">
            {t("attestations.held.columns.source")}
          </dt>
          <dd className="text-ink capitalize">{held.source}</dd>
          <dt className="text-ink-soft">
            {t("attestations.held.columns.received")}
          </dt>
          <dd className="text-ink">{formatWhen(held.receivedAt)}</dd>
          <dt className="text-ink-soft">
            {t("attestations.held.detail.type")}
          </dt>
          <dd className="text-ink-soft font-mono text-[12px] break-all">
            {held.vct}
          </dd>
        </dl>

        <div>
          <h3 className="text-ink mb-2 text-[13px] font-semibold">
            {t("attestations.held.detail.attributes")}
          </h3>
          {claims.isError ? (
            <p className="text-error text-[13px]">
              {t("attestations.loadError", { message: claims.error.message })}
            </p>
          ) : claims.isPending ? (
            <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
          ) : attributes.length === 0 ? (
            <p className="text-ink-soft text-[13px]">
              {t("attestations.held.detail.noAttributes")}
            </p>
          ) : (
            <dl className="border-line divide-line divide-y rounded-md border">
              {attributes.map((attribute) => (
                <div
                  key={attribute.key}
                  className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-4 px-3 py-2"
                >
                  <dt className="text-ink-soft truncate text-[12.5px]">
                    {attribute.label || attribute.key}
                  </dt>
                  <dd className="text-ink text-[13px] break-words">
                    {formatClaimValue(attribute.value)}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      </div>
    </Modal>
  );
}

// formatClaimValue renders a disclosed SD-JWT claim value for display. Primitives
// show as text; objects/arrays are JSON-stringified so nested claims stay legible.
function formatClaimValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "—";
  }
  if (typeof value === "string") {
    return value;
  }
  if (
    typeof value === "number" ||
    typeof value === "boolean" ||
    typeof value === "bigint"
  ) {
    return String(value);
  }
  return JSON.stringify(value);
}

function SchemasTab({
  slug,
  schemas,
  pending,
  error,
  onCreate,
  onEdit,
}: {
  slug: string;
  schemas: AttestationSchema[];
  pending: boolean;
  error: Error | null;
  onCreate: () => void;
  onEdit: (schema: AttestationSchema) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteAttestationSchemaMutation(slug);
  const [pendingDelete, setPendingDelete] = useState<AttestationSchema | null>(
    null,
  );

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.loadError", { message: error.message })}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div>
        <Button icon="add" onClick={onCreate}>
          {t("attestations.schemas.newAction")}
        </Button>
      </div>
      {pending ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        </Card>
      ) : schemas.length === 0 ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">
            {t("attestations.schemas.empty")}
          </p>
        </Card>
      ) : (
        <div className="flex flex-col gap-3">
          {schemas.map((schema) => (
            <Card key={schema.id} className="flex flex-col gap-2.5 p-4">
              <div className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 items-center gap-3">
                  {schema.logoUri && (
                    <img
                      src={schema.logoUri}
                      alt={t("attestations.credentialImageAlt")}
                      className="border-line bg-surface h-10 w-10 shrink-0 rounded-md border object-contain"
                    />
                  )}
                  <div className="min-w-0">
                    <div className="text-ink truncate font-semibold">
                      {schema.displayName}
                    </div>
                    <div className="text-ink-soft truncate font-mono text-[12px]">
                      {schema.vct}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  {schema.qualified && (
                    <Tag tone="blue">{t("attestations.qualified")}</Tag>
                  )}
                  <Button
                    variant="ghost"
                    size="sm"
                    icon="edit"
                    iconOnly
                    onClick={() => onEdit(schema)}
                    aria-label={t("common.edit")}
                  />
                  <Button
                    variant="dangerGhost"
                    size="sm"
                    icon="delete"
                    iconOnly
                    onClick={() => setPendingDelete(schema)}
                    aria-label={t("attestations.schemas.delete")}
                  />
                </div>
              </div>
              <div className="flex flex-wrap gap-1.5">
                {schema.attributes.map((attribute) => (
                  <span
                    key={attribute.key}
                    className="bg-surface-3 text-ink-soft rounded-full px-2 py-0.5 text-[11.5px] font-medium"
                  >
                    {attribute.label || attribute.key}
                  </span>
                ))}
              </div>
            </Card>
          ))}
        </div>
      )}
      {pendingDelete && (
        <ConfirmDialog
          title={t("attestations.schemas.delete")}
          message={t("attestations.schemas.confirmDelete", {
            name: pendingDelete.displayName,
          })}
          confirmLabel={t("attestations.schemas.delete")}
          busy={remove.isPending}
          onConfirm={() => {
            remove.mutate({ schemaId: pendingDelete.id });
            setPendingDelete(null);
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}
