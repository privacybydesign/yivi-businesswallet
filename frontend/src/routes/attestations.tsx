import { useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationKey,
  AttestationSchema,
  AttestationTemplate,
  ClientAttribute,
  HeldAttestation,
  IssuedAttestation,
} from "../api/attestations";
import { attributeValueText, localized } from "../api/attestations";
import {
  useAttestationKeysQuery,
  useAttestationSchemasQuery,
  useAttestationTemplatesQuery,
  useCreateAttestationKeyMutation,
  useDeleteAttestationSchemaMutation,
  useDeleteAttestationTemplateMutation,
  useDeleteHeldAttestationMutation,
  useHeldAttestationsQuery,
  useIssuedAttestationsQuery,
  useRevokeAttestationKeyMutation,
  useRevokeIssuedAttestationMutation,
  useSuspendAttestationKeyMutation,
} from "../api/attestations.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card, Icon, Modal, Table, Tag, TopBar } from "../ui";
import type { IconName } from "../ui";
import { AttestationIssueWizard } from "./attestations-issue";
import { AttestationSchemaForm } from "./attestations-schema-form";
import { AttestationTemplateForm } from "./attestations-template-form";
import { control } from "../lib/attestation-form";

const ISSUED_COLUMN_COUNT = 5;
const CHIP_LIMIT = 3;
const ADMIN_ROLE = "admin";

const KIND_WALLET = "wallet_managed";
const KIND_QUALIFIED = "qualified_certificate";
const KEY_KINDS = [KIND_WALLET, KIND_QUALIFIED] as const;

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

type Tab = "templates" | "issued" | "wallet" | "schemas" | "keys";

const ADMIN_TABS: readonly Tab[] = [
  "templates",
  "issued",
  "wallet",
  "schemas",
  "keys",
];
const MEMBER_TABS: readonly Tab[] = ["issued", "wallet"];

function readTab(params: URLSearchParams, tabs: readonly Tab[]): Tab {
  const value = params.get("tab");
  return tabs.find((tab) => tab === value) ?? tabs[0];
}

// The modal currently open, if any.
type ActiveModal =
  | { kind: "issue"; template?: AttestationTemplate }
  | { kind: "schema"; schema?: AttestationSchema }
  | { kind: "template"; template?: AttestationTemplate }
  | { kind: "credential"; view: HeldAttestation }
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

  const enabled = !org.isError;
  const issued = useIssuedAttestationsQuery(slug, enabled);
  const wallet = useHeldAttestationsQuery(slug, enabled);
  const templates = useAttestationTemplatesQuery(slug, enabled && isAdmin);
  const schemas = useAttestationSchemasQuery(slug, enabled && isAdmin);
  const keys = useAttestationKeysQuery(slug, enabled && isAdmin);

  const formatWhen = useWhenFormatter();

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
            {tab === "templates" && (
              <TemplatesTab
                slug={slug}
                templates={templates.data ?? []}
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

            {tab === "wallet" && (
              <WalletTab
                slug={slug}
                rows={wallet.data ?? []}
                pending={wallet.isPending}
                error={wallet.error}
                isAdmin={isAdmin}
                formatWhen={formatWhen}
                onOpen={(view) => setModal({ kind: "credential", view })}
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

            {tab === "keys" && (
              <KeysTab
                slug={slug}
                keys={keys.data ?? []}
                pending={keys.isPending}
                error={keys.error}
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
      {modal?.kind === "credential" && (
        <CredentialDetailModal
          view={modal.view}
          formatWhen={formatWhen}
          onClose={() => setModal(null)}
        />
      )}
    </>
  );
}

const TAB_LABEL_KEYS = {
  templates: "attestations.tabs.templates",
  issued: "attestations.tabs.issued",
  wallet: "attestations.tabs.wallet",
  schemas: "attestations.tabs.schemas",
  keys: "attestations.tabs.keys",
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
  pending,
  error,
  onIssue,
  onEdit,
}: {
  slug: string;
  templates: AttestationTemplate[];
  pending: boolean;
  error: Error | null;
  onIssue: (template: AttestationTemplate) => void;
  onEdit: (template: AttestationTemplate) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteAttestationTemplateMutation(slug);

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
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
      {templates.map((template) => {
        const chips = template.attributes.slice(0, CHIP_LIMIT);
        const extra = template.attributes.length - chips.length;
        return (
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
                  {t("attestations.templates.moreAttributes", { count: extra })}
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
                  onClick={() => {
                    if (
                      window.confirm(
                        t("attestations.templates.confirmDelete", {
                          name: template.name,
                        }),
                      )
                    ) {
                      remove.mutate({ templateId: template.id });
                    }
                  }}
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

const WALLET_COLUMN_COUNT = 5;

// attributeLabel is the localized display name for an attribute, falling back to
// its claim path (e.g. ["address","street"] → "address.street").
function attributeLabel(attr: ClientAttribute): string {
  return localized(attr.display_name, attr.claim_path.map(String).join("."));
}

function WalletTab({
  slug,
  rows,
  pending,
  error,
  isAdmin,
  formatWhen,
  onOpen,
}: {
  slug: string;
  rows: HeldAttestation[];
  pending: boolean;
  error: Error | null;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
  onOpen: (view: HeldAttestation) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const remove = useDeleteHeldAttestationMutation(slug);
  const columnCount = isAdmin ? WALLET_COLUMN_COUNT : WALLET_COLUMN_COUNT - 1;

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
            {t("attestations.wallet.columns.credential")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[24%]">
            {t("attestations.wallet.columns.issuer")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.wallet.columns.source")}
          </Table.HeaderCell>
          <Table.HeaderCell className="w-[16%]">
            {t("attestations.wallet.columns.received")}
          </Table.HeaderCell>
          {isAdmin && (
            <Table.HeaderCell className="w-[12%]" srOnly>
              {t("attestations.wallet.columns.actions")}
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
              {t("attestations.wallet.empty")}
            </Table.State>
          ) : (
            rows.map((row) => {
              const cred = row.credential;
              const name = localized(cred.name, cred.credential_id);
              const issuerName = localized(cred.issuer.name, cred.issuer.id);
              return (
                <Table.Row
                  key={row.heldId}
                  role="button"
                  tabIndex={0}
                  title={t("attestations.wallet.rowHint")}
                  onClick={() => onOpen(row)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onOpen(row);
                    }
                  }}
                  className="hover:bg-surface-2 focus-visible:bg-surface-2 cursor-pointer outline-none"
                >
                  <Table.Cell>
                    <div className="flex items-center gap-2">
                      {cred.image?.base64 && (
                        <img
                          src={`data:${cred.image.mime_type ?? "image/png"};base64,${cred.image.base64}`}
                          alt=""
                          className="size-6 shrink-0 rounded object-contain"
                        />
                      )}
                      <div className="min-w-0">
                        <div className="text-ink truncate">{name}</div>
                        <div className="text-ink-soft truncate font-mono text-[11px]">
                          {cred.credential_id}
                        </div>
                      </div>
                    </div>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft truncate">
                    {issuerName}
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
                        onClick={(e) => {
                          e.stopPropagation();
                          if (
                            window.confirm(
                              t("attestations.wallet.confirmDelete", {
                                name,
                              }),
                            )
                          ) {
                            remove.mutate({ heldId: row.heldId });
                          }
                        }}
                      >
                        {t("attestations.wallet.delete")}
                      </Button>
                    </Table.Cell>
                  )}
                </Table.Row>
              );
            })
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}

// CredentialDetailModal shows one held credential's full display model: logo,
// name, issuer, provenance/dates, and the disclosed attributes. Opened from a
// wallet row — a single credential type can have several instances, so each row
// is its own credential.
function CredentialDetailModal({
  view,
  formatWhen,
  onClose,
}: {
  view: HeldAttestation;
  formatWhen: (iso: string) => string;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const cred = view.credential;
  const name = localized(cred.name, cred.credential_id);
  const issuerName = localized(cred.issuer.name, cred.issuer.id);
  const attributes = cred.attributes ?? [];

  const unixWhen = (sec: number | null | undefined): string | null =>
    sec ? formatWhen(new Date(sec * 1000).toISOString()) : null;
  const issued = unixWhen(cred.issuance_date);
  const expires = unixWhen(cred.expiry_date);

  return (
    <Modal title={name} closeLabel={t("common.close")} onClose={onClose}>
      <div className="flex flex-col gap-5">
        <div className="flex items-center gap-3">
          {cred.image?.base64 && (
            <img
              src={`data:${cred.image.mime_type ?? "image/png"};base64,${cred.image.base64}`}
              alt=""
              className="size-10 shrink-0 rounded object-contain"
            />
          )}
          <div className="min-w-0">
            <div className="text-ink truncate text-[15px] font-semibold">
              {name}
            </div>
            <div className="text-ink-soft truncate font-mono text-[11px]">
              {cred.credential_id}
            </div>
          </div>
        </div>

        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-[13px]">
          <DetailRow
            label={t("attestations.wallet.details.issuer")}
            value={issuerName}
          />
          <DetailRow
            label={t("attestations.wallet.details.source")}
            value={<span className="capitalize">{view.source}</span>}
          />
          <DetailRow
            label={t("attestations.wallet.details.received")}
            value={formatWhen(view.receivedAt)}
          />
          {issued && (
            <DetailRow
              label={t("attestations.wallet.details.issued")}
              value={issued}
            />
          )}
          {expires && (
            <DetailRow
              label={t("attestations.wallet.details.expires")}
              value={expires}
            />
          )}
        </dl>

        <div>
          <div className="text-ink mb-2 text-[13px] font-semibold">
            {t("attestations.wallet.details.attributes")}
          </div>
          {attributes.length === 0 ? (
            <p className="text-ink-soft text-[13px]">
              {t("attestations.wallet.details.noAttributes")}
            </p>
          ) : (
            <dl className="border-line divide-line rounded-yivi divide-y border">
              {attributes.map((attr, i) => (
                <div
                  key={i}
                  className="grid grid-cols-[40%_1fr] gap-3 px-3.5 py-2"
                >
                  <dt className="text-ink-soft truncate text-[12.5px]">
                    {attributeLabel(attr)}
                  </dt>
                  <dd className="text-ink text-[13px] break-words">
                    {attributeValueText(attr)}
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

function DetailRow({
  label,
  value,
}: {
  label: string;
  value: React.ReactNode;
}): React.JSX.Element {
  return (
    <>
      <dt className="text-ink-soft">{label}</dt>
      <dd className="text-ink min-w-0 break-words">{value}</dd>
    </>
  );
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
                <div className="min-w-0">
                  <div className="text-ink truncate font-semibold">
                    {schema.displayName}
                  </div>
                  <div className="text-ink-soft truncate font-mono text-[12px]">
                    {schema.vct}
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
                    onClick={() => {
                      if (
                        window.confirm(
                          t("attestations.schemas.confirmDelete", {
                            name: schema.displayName,
                          }),
                        )
                      ) {
                        remove.mutate({ schemaId: schema.id });
                      }
                    }}
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
    </div>
  );
}

function KeysTab({
  slug,
  keys,
  pending,
  error,
}: {
  slug: string;
  keys: AttestationKey[];
  pending: boolean;
  error: Error | null;
}): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateAttestationKeyMutation(slug);
  const suspend = useSuspendAttestationKeyMutation(slug);
  const revoke = useRevokeAttestationKeyMutation(slug);

  const [label, setLabel] = useState("");
  const [kind, setKind] = useState<(typeof KEY_KINDS)[number]>(KIND_WALLET);
  const [providerRef, setProviderRef] = useState("");

  function handleAdd(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (create.isPending || label.trim() === "") {
      return;
    }
    create.mutate(
      {
        kind,
        label: label.trim(),
        providerRef: providerRef.trim() || undefined,
      },
      {
        onSuccess: () => {
          setLabel("");
          setProviderRef("");
        },
      },
    );
  }

  const kindIcon: IconName = "lock";

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.loadError", { message: error.message })}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <Card className="p-4">
        <form
          onSubmit={handleAdd}
          className="flex flex-col gap-3 md:flex-row md:items-end"
        >
          <div className="flex flex-1 flex-col gap-1">
            <label
              htmlFor="key-label"
              className="text-ink-soft text-[12px] font-semibold"
            >
              {t("attestations.keys.labelLabel")}
            </label>
            <input
              id="key-label"
              className={`${control(false)} h-9`}
              value={label}
              onChange={(event) => setLabel(event.target.value)}
              placeholder={t("attestations.keys.labelPlaceholder")}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label
              htmlFor="key-kind"
              className="text-ink-soft text-[12px] font-semibold"
            >
              {t("attestations.keys.kindLabel")}
            </label>
            <select
              id="key-kind"
              className={`${control(false)} h-9`}
              value={kind}
              onChange={(event) =>
                setKind(event.target.value as (typeof KEY_KINDS)[number])
              }
            >
              {KEY_KINDS.map((value) => (
                <option key={value} value={value}>
                  {value === KIND_WALLET
                    ? t("attestations.keys.kinds.walletManaged")
                    : t("attestations.keys.kinds.qualifiedCertificate")}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-1 flex-col gap-1">
            <label
              htmlFor="key-provider"
              className="text-ink-soft text-[12px] font-semibold"
            >
              {t("attestations.keys.providerRef")}
            </label>
            <input
              id="key-provider"
              className={`${control(false)} h-9`}
              value={providerRef}
              onChange={(event) => setProviderRef(event.target.value)}
            />
          </div>
          <Button type="submit" icon="add" loading={create.isPending}>
            {t("attestations.keys.add")}
          </Button>
        </form>
        {create.isError && create.error && (
          <p role="alert" className="text-error mt-2 text-[12px]">
            {t("attestations.keys.error", { message: create.error.message })}
          </p>
        )}
      </Card>

      {pending ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        </Card>
      ) : keys.length === 0 ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">
            {t("attestations.keys.empty")}
          </p>
        </Card>
      ) : (
        <div className="flex flex-col gap-2.5">
          {keys.map((key) => (
            <Card key={key.id} className="flex items-center gap-3 p-4">
              <span className="bg-surface-3 flex h-8 w-8 shrink-0 items-center justify-center rounded-md">
                <Icon name={kindIcon} size={14} className="text-ink-soft" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-ink truncate font-semibold">
                  {key.label}
                </div>
                <div className="text-ink-soft text-[12.5px]">
                  {key.kind === KIND_WALLET
                    ? t("attestations.keys.kinds.walletManaged")
                    : t("attestations.keys.kinds.qualifiedCertificate")}
                </div>
              </div>
              <Tag tone={key.status === "active" ? "green" : "default"} dot>
                <span className="capitalize">{key.status}</span>
              </Tag>
              {key.status !== "revoked" && (
                <div className="flex items-center gap-1">
                  {key.status === "active" && (
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => suspend.mutate({ keyId: key.id })}
                    >
                      {t("attestations.keys.suspend")}
                    </Button>
                  )}
                  <Button
                    variant="dangerGhost"
                    size="sm"
                    onClick={() => revoke.mutate({ keyId: key.id })}
                  >
                    {t("attestations.keys.revoke")}
                  </Button>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
