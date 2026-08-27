import { useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationSchema,
  AttestationTemplate,
  CredentialOffer,
  HeldAttestation,
  IssuedAttestation,
} from "../api/attestations";
import { HELD_SOURCES } from "../api/attestations";
import {
  useAcceptCredentialOfferMutation,
  useAttestationKeysQuery,
  useAttestationSchemasQuery,
  useAttestationTemplatesQuery,
  useCancelIssuedAttestationMutation,
  useCredentialOffersQuery,
  useDeclineCredentialOfferMutation,
  useDeleteAttestationSchemaMutation,
  useDeleteAttestationTemplateMutation,
  useDeleteHeldAttestationMutation,
  useHeldAttestationsQuery,
  useIssuedAttestationsQuery,
  useRevokeIssuedAttestationMutation,
} from "../api/attestations.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { credentialDisplayName } from "../lib/credential-display";
import { useDateFormatter, useWhenFormatter } from "../lib/format-when";
import type {
  HeldCredentialWithStatus,
  HeldSourceFilter,
  HeldStatus,
  HeldStatusFilter,
} from "../lib/held-credential";
import {
  HELD_SOURCE_FILTERS,
  HELD_STATUS_FILTERS,
  HELD_STATUS_TONES,
  heldExpiryAt,
  heldExpiryIsPast,
  heldSections,
  heldSourceLabel,
  heldStatusLabel,
} from "../lib/held-credential";
import { useDebouncedValue } from "../lib/use-debounced-value";
import { Button, Card, ConfirmDialog, Input, Table, Tag, TopBar } from "../ui";
import { AttestationIssueWizard } from "./attestations-issue";
import { AttestationSchemaForm } from "./attestations-schema-form";
import { AttestationTemplateForm } from "./attestations-template-form";
import { WscaActivationNotice } from "./wsca-activation-notice";

const ISSUED_COLUMN_COUNT = 5;
const CHIP_LIMIT = 3;
const ADMIN_ROLE = "admin";
const SEARCH_DEBOUNCE_MS = 300;

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
    case "cancelled":
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

  const enabled = !org.isError;
  const issued = useIssuedAttestationsQuery(slug, enabled);
  const held = useHeldAttestationsQuery(slug, enabled);
  const offers = useCredentialOffersQuery(slug, enabled);
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
              <>
                {/* Above the wallet, and outside HeldTab: an offer must still be
                    visible to an org that holds nothing yet. */}
                <OffersSection
                  slug={slug}
                  offers={offers.data ?? []}
                  error={offers.error}
                  isAdmin={isAdmin}
                  formatWhen={formatWhen}
                />
                <HeldTab
                  slug={slug}
                  rows={held.data ?? []}
                  pending={held.isPending}
                  error={held.error}
                  isAdmin={isAdmin}
                  formatWhen={formatWhen}
                />
              </>
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
  const cancel = useCancelIssuedAttestationMutation(slug);
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
                    {row.status === "offered" && (
                      <Button
                        variant="dangerGhost"
                        size="sm"
                        onClick={() => cancel.mutate({ issuedId: row.id })}
                      >
                        {t("attestations.issued.cancel")}
                      </Button>
                    )}
                    {row.status === "claimed" && (
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

// Toolbar controls: the two filter dropdowns share the app's form-control styling.
const FILTER_SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 border px-3 text-[13.5px] transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3";

// The credential offers waiting on the organization. An offer that arrived over
// QERDS is not in the wallet yet — accepting is what redeems it, declining leaves
// it unredeemed for good. Renders nothing when there is nothing to decide, so the
// Wallet tab is unchanged for an org with no inbound offers.
function OffersSection({
  slug,
  offers,
  error,
  isAdmin,
  formatWhen,
}: {
  slug: string;
  offers: CredentialOffer[];
  error: Error | null;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const accept = useAcceptCredentialOfferMutation(slug);
  const decline = useDeclineCredentialOfferMutation(slug);
  const [pendingDecline, setPendingDecline] = useState<CredentialOffer | null>(
    null,
  );
  // A decision in flight blocks the others: only one offer is being decided at a
  // time, and the spinner belongs to the offer whose accept is actually running.
  const busy = accept.isPending || decline.isPending;

  if (error) {
    return (
      <ErrorCard
        message={t("attestations.offers.loadError", { message: error.message })}
      />
    );
  }
  if (offers.length === 0) {
    return null;
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-ink text-[14px] font-semibold">
          {t("attestations.offers.title")}
        </h2>
        <span className="text-muted text-[12.5px]">{offers.length}</span>
      </div>
      <p className="text-ink-soft text-[13px]">
        {t("attestations.offers.description")}
      </p>
      <div className="flex flex-col gap-3">
        {offers.map((offer) => (
          <Card key={offer.id} className="flex flex-col gap-3 p-4">
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <div className="text-ink truncate font-semibold">
                  {offer.credentialName ||
                    t("attestations.offers.unnamedCredential")}
                </div>
                <div className="text-ink-soft truncate text-[12.5px]">
                  {t("attestations.offers.from", {
                    sender: offer.senderOrgName || offer.senderAddress,
                  })}
                </div>
              </div>
              <Tag tone="amber" dot>
                {t("attestations.offers.pending")}
              </Tag>
            </div>

            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-ink-soft text-[12.5px]">
                {formatWhen(offer.receivedAt)}
              </span>
              {isAdmin && (
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => setPendingDecline(offer)}
                  >
                    {t("attestations.offers.decline")}
                  </Button>
                  <Button
                    size="sm"
                    loading={
                      accept.isPending && accept.variables?.offerId === offer.id
                    }
                    disabled={busy}
                    onClick={() => accept.mutate({ offerId: offer.id })}
                  >
                    {t("attestations.offers.accept")}
                  </Button>
                </div>
              )}
            </div>
          </Card>
        ))}
      </div>

      {pendingDecline && (
        <ConfirmDialog
          title={t("attestations.offers.decline")}
          message={t("attestations.offers.confirmDecline", {
            name:
              pendingDecline.credentialName ||
              t("attestations.offers.unnamedCredential"),
            sender:
              pendingDecline.senderOrgName || pendingDecline.senderAddress,
          })}
          confirmLabel={t("attestations.offers.decline")}
          busy={decline.isPending}
          onConfirm={() => {
            decline.mutate({ offerId: pendingDecline.id });
            setPendingDecline(null);
          }}
          onClose={() => setPendingDecline(null)}
        />
      )}
    </section>
  );
}

function readHeldStatusFilter(params: URLSearchParams): HeldStatusFilter {
  const raw = params.get("status") as HeldStatusFilter | null;
  return raw && HELD_STATUS_FILTERS.includes(raw) ? raw : "";
}

function readHeldSourceFilter(params: URLSearchParams): HeldSourceFilter {
  const raw = params.get("source") as HeldSourceFilter | null;
  return raw && HELD_SOURCE_FILTERS.includes(raw) ? raw : "";
}

// The credentials the organization holds, as cards grouped into what needs
// attention (revoked, expired or expiring soon) and what is valid. A card opens
// the credential's detail page; the search term and both filters live in the URL
// alongside ?tab=held so the view survives a refresh and can be shared.
function HeldTab({
  slug,
  rows,
  pending,
  error,
  isAdmin,
  formatWhen,
}: {
  slug: string;
  rows: HeldAttestation[];
  pending: boolean;
  error: Error | null;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatDate = useDateFormatter();
  const remove = useDeleteHeldAttestationMutation(slug);
  const [pendingDelete, setPendingDelete] = useState<HeldAttestation | null>(
    null,
  );

  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q")?.trim() ?? "";
  const status = readHeldStatusFilter(searchParams);
  const source = readHeldSourceFilter(searchParams);

  const [searchInput, setSearchInput] = useState(
    () => searchParams.get("q") ?? "",
  );
  const debouncedSearch = useDebouncedValue(
    searchInput.trim(),
    SEARCH_DEBOUNCE_MS,
  );

  // The debounced term is pushed to the URL (history-replaced so typing doesn't
  // flood Back); the guard avoids rewriting the URL it just read.
  React.useEffect(() => {
    if (debouncedSearch === query) return;
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (debouncedSearch) next.set("q", debouncedSearch);
        else next.delete("q");
        return next;
      },
      { replace: true },
    );
  }, [debouncedSearch, query, setSearchParams]);

  const setFilter = (key: "status" | "source", value: string): void => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });
  };

  const resetView = (): void => {
    setSearchInput("");
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      for (const key of ["q", "status", "source"]) next.delete(key);
      return next;
    });
  };

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
  if (rows.length === 0) {
    return (
      <Card className="p-6">
        <p className="text-ink-soft text-[14px]">
          {t("attestations.held.empty")}
        </p>
      </Card>
    );
  }

  const filtered = query !== "" || status !== "" || source !== "";
  // One instant for the whole render, so a card's expiry tense cannot disagree with
  // the section the same credential was sorted into.
  const now = new Date();
  const sections = heldSections(rows, { query, status, source }, now);
  const nothingMatches =
    sections.attention.length === 0 && sections.valid.length === 0;

  const renderCard = ({
    credential,
    status: cardStatus,
  }: HeldCredentialWithStatus): React.JSX.Element => (
    <HeldCard
      key={credential.id}
      slug={slug}
      credential={credential}
      status={cardStatus}
      now={now}
      isAdmin={isAdmin}
      formatWhen={formatWhen}
      formatDate={formatDate}
      onDelete={() => setPendingDelete(credential)}
    />
  );

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="w-full max-w-[320px]">
          <Input
            icon="search"
            placeholder={t("attestations.held.search")}
            value={searchInput}
            onChange={(event) => setSearchInput(event.target.value)}
            aria-label={t("attestations.held.search")}
          />
        </div>
        <select
          className={FILTER_SELECT_CLASS}
          value={status}
          aria-label={t("attestations.held.filters.status")}
          onChange={(event) => setFilter("status", event.target.value)}
        >
          <option value="">{t("attestations.held.filters.allStatuses")}</option>
          <option value="attention">
            {t("attestations.held.filters.attention")}
          </option>
          <option value="revoked">
            {t("attestations.held.status.revoked")}
          </option>
          <option value="expired">
            {t("attestations.held.status.expired")}
          </option>
          <option value="expiringSoon">
            {t("attestations.held.status.expiringSoon")}
          </option>
          <option value="valid">{t("attestations.held.status.valid")}</option>
        </select>
        <select
          className={FILTER_SELECT_CLASS}
          value={source}
          aria-label={t("attestations.held.filters.source")}
          onChange={(event) => setFilter("source", event.target.value)}
        >
          <option value="">{t("attestations.held.filters.allSources")}</option>
          {HELD_SOURCES.map((value) => (
            <option key={value} value={value}>
              {heldSourceLabel(value, t)}
            </option>
          ))}
        </select>
        {filtered && (
          <Button variant="ghost" size="sm" onClick={resetView}>
            {t("attestations.held.reset")}
          </Button>
        )}
      </div>

      {nothingMatches ? (
        <Card className="p-6">
          <p className="text-ink-soft text-[14px]">
            {t("attestations.held.noMatch")}
          </p>
        </Card>
      ) : (
        <>
          {sections.attention.length > 0 && (
            <HeldSection
              title={t("attestations.held.sections.attention")}
              count={sections.attention.length}
            >
              {sections.attention.map(renderCard)}
            </HeldSection>
          )}
          {sections.valid.length > 0 && (
            <HeldSection
              title={t("attestations.held.sections.valid")}
              count={sections.valid.length}
            >
              {sections.valid.map(renderCard)}
            </HeldSection>
          )}
        </>
      )}

      {pendingDelete && (
        <ConfirmDialog
          title={t("attestations.held.delete")}
          message={t("attestations.held.confirmDelete", {
            name:
              pendingDelete.displayName ||
              credentialDisplayName(pendingDelete.vct),
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
    </div>
  );
}

// One horizontal section of held-credential cards, headed by its name and count.
function HeldSection({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-ink text-[14px] font-semibold">{title}</h2>
        <span className="text-muted text-[12.5px]">{count}</span>
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {children}
      </div>
    </section>
  );
}

// One held credential as a card, matching the template cards: logo, name and mono
// vct, its status pinned top-right, provenance below. The whole card is the link
// to the credential's detail page — an overlay stretched over it — so the delete
// action sits above that overlay to stay clickable in its own right.
function HeldCard({
  slug,
  credential,
  status,
  now,
  isAdmin,
  formatWhen,
  formatDate,
  onDelete,
}: {
  slug: string;
  credential: HeldAttestation;
  status: HeldStatus;
  now: Date;
  isAdmin: boolean;
  formatWhen: (iso: string) => string;
  formatDate: (iso: string) => string;
  onDelete: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const name = credential.displayName || credentialDisplayName(credential.vct);
  // The expiry line only renders for a date the view can phrase. A value that does
  // not parse is dropped rather than echoed verbatim, which is how heldStatus reads
  // it too: as a credential that does not expire.
  const expiresAt =
    heldExpiryAt(credential) === null ? undefined : credential.expiresAt;

  return (
    <Card className="focus-within:border-ink focus-within:ring-ink/10 hover:border-line-strong relative flex flex-col gap-3 p-4 transition-colors focus-within:ring-3">
      <Link
        to={`/${slug}/attestations/held/${credential.id}`}
        aria-label={t("attestations.held.viewDetail", { name })}
        className="rounded-yivi absolute inset-0 outline-none"
      />
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-3">
          {credential.logoUri && (
            <img
              src={credential.logoUri}
              alt={t("attestations.credentialImageAlt")}
              className="border-line bg-surface h-10 w-10 shrink-0 rounded-md border object-contain"
            />
          )}
          <div className="min-w-0">
            <div className="text-ink truncate font-semibold">{name}</div>
            <div className="text-ink-soft truncate font-mono text-[12px]">
              {credential.vct}
            </div>
          </div>
        </div>
        <Tag tone={HELD_STATUS_TONES[status]} dot>
          {heldStatusLabel(status, t)}
        </Tag>
      </div>

      <div className="flex flex-col gap-0.5 text-[12.5px]">
        <div className="text-ink-soft truncate">
          <span className="text-muted">
            {t("attestations.held.fields.issuer")}
          </span>{" "}
          {credential.issuerName || credential.issuer}
        </div>
        {expiresAt && (
          <div className="text-ink-soft">
            {heldExpiryIsPast(credential, now)
              ? t("attestations.held.expiredOn", {
                  date: formatDate(expiresAt),
                })
              : t("attestations.held.expires", {
                  date: formatDate(expiresAt),
                })}
          </div>
        )}
      </div>

      <div className="mt-auto flex items-center justify-between gap-2 pt-1">
        <div className="flex min-w-0 items-center gap-2">
          <Tag>{heldSourceLabel(credential.source, t)}</Tag>
          <span className="text-ink-soft truncate text-[12.5px]">
            {formatWhen(credential.receivedAt)}
          </span>
        </div>
        {isAdmin && (
          <Button
            variant="dangerGhost"
            size="sm"
            // Above the link overlay, so removing a credential is not a click
            // through to its detail page.
            className="relative z-10"
            onClick={onDelete}
          >
            {t("attestations.held.delete")}
          </Button>
        )}
      </div>
    </Card>
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
