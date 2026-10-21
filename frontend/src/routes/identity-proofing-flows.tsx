import { useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingFlowsQuery,
  useProofingStatsQuery,
  useSetFlowSelectionMutation,
} from "../api/identity-proofing.queries";
import type { ProofingFlow } from "../api/identity-proofing";
import {
  assuranceLevelLabel,
  editedFlowSelection,
  flowStepLabels,
  proofingErrorMessage,
  proofingStatsBy,
} from "../lib/identity-proofing";
import { Button, Card, Icon, StatusText, Table, TopBar } from "../ui";
import { FlowEditor } from "./flow-editor";

const ERROR = "text-error text-[12.5px]";
const OPEN_ICON_SIZE = 16;

// The org admin's identity proofing flows: what each one requests and at which
// minimum assurance level, how many people it identified lately, which flow a
// request form starts on and which flows members may use. A flow opens to its
// own page: the people it identified, its versions and its editor.
export default function IdentityProofingFlows(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const flows = useProofingFlowsQuery(slug, isAdmin);
  const [creating, setCreating] = useState(false);

  return (
    <>
      <TopBar
        title={t("identityProofingFlows.title")}
        subtitle={t("identityProofingFlows.subtitle")}
        actions={
          isAdmin &&
          !creating && (
            <Button icon="add" onClick={() => setCreating(true)}>
              {t("identityProofingFlows.new.title")}
            </Button>
          )
        }
      />
      <div className="flex flex-col gap-6 p-4 sm:p-8">
        {org.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : !isAdmin ? (
          <p className="text-ink-soft text-[14px]">
            {t("identityProofingFlows.adminOnly")}
          </p>
        ) : flows.isPending ? (
          <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
        ) : flows.isError ? (
          <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>
        ) : (
          <>
            {creating && (
              <FlowEditor
                slug={slug}
                mode={{ kind: "new" }}
                onDone={() => setCreating(false)}
              />
            )}
            <FlowsTable slug={slug} flows={flows.data} />
            <DataRequestsNote slug={slug} />
          </>
        )}
      </div>
    </>
  );
}

function FlowsTable({
  slug,
  flows,
}: {
  slug: string;
  flows: ProofingFlow[];
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSetFlowSelectionMutation(slug);
  const stats = useProofingStatsQuery(slug);
  const perFlow = proofingStatsBy(stats.data?.rows ?? [], (r) => r.flowId);
  // Seeded once from the server; a flow created later starts unselected.
  const [allowed, setAllowed] = useState<ReadonlySet<string>>(
    () => new Set(flows.filter((f) => f.allowed).map((f) => f.id)),
  );
  const [defaultId, setDefaultId] = useState(
    () => flows.find((f) => f.default)?.id ?? "",
  );

  // A flow that can no longer be ticked drops out. The default is any flow an
  // external person can be sent, whether or not members may use it. The list
  // holds no data request flow.
  const selectable = flows.filter((f) => f.completable);
  const { selection, dirty } = editedFlowSelection(
    selectable,
    allowed,
    defaultId,
    {
      flowIds: flows.filter((f) => f.allowed).map((f) => f.id),
      defaultFlowId: flows.find((f) => f.default)?.id ?? "",
    },
  );

  function toggle(id: string, on: boolean): void {
    setAllowed((current) => {
      const next = new Set(current);
      if (on) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    save.mutate(selection);
  }

  if (flows.length === 0) {
    return (
      <p className="text-ink-soft text-[13px]">
        {t("identityProofingFlows.selection.empty")}
      </p>
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
      <Card>
        <Table>
          <Table.Head>
            <Table.HeaderCell scope="col">
              {t("identityProofingFlows.columns.flow")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofingFlows.columns.assurance")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col" className="text-right">
              {t("identityProofingFlows.columns.people")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofingFlows.columns.preselected")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col">
              {t("identityProofingFlows.columns.members")}
            </Table.HeaderCell>
            <Table.HeaderCell scope="col" srOnly>
              {t("identityProofingFlows.columns.open")}
            </Table.HeaderCell>
          </Table.Head>
          <Table.Body>
            {flows.map((flow) => {
              const nameId = `proofing-flow-name-${flow.id}`;
              const canSelect = selectable.includes(flow);
              return (
                <Table.Row key={flow.id}>
                  <Table.Cell>
                    <div className="flex flex-col gap-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <Link
                          id={nameId}
                          to={flowPath(slug, flow.id)}
                          className="text-ink text-[15px] font-semibold hover:underline"
                        >
                          {flow.name}
                        </Link>
                        {!flow.completable && (
                          <StatusText tone="amber">
                            {t("identityProofingFlows.notCompletable")}
                          </StatusText>
                        )}
                      </div>
                      <div className="text-ink-soft text-[12.5px]">
                        {[
                          t("identityProofingFlows.versionLong", {
                            version: flow.version,
                          }),
                          flowStepLabels(flow, t).join(", "),
                        ]
                          .filter((part) => part !== "")
                          .join(" · ")}
                      </div>
                    </div>
                  </Table.Cell>
                  <Table.Cell>
                    {flow.requiredAssuranceLevel
                      ? assuranceLevelLabel(flow.requiredAssuranceLevel, t)
                      : t("identityProofing.flows.noAssurance")}
                  </Table.Cell>
                  <Table.Cell className="text-right font-semibold">
                    {stats.data ? (perFlow.get(flow.id)?.sessions ?? 0) : "—"}
                  </Table.Cell>
                  <Table.Cell>
                    <label className="inline-flex items-center gap-2">
                      <input
                        type="radio"
                        name="proofing-default-flow"
                        className="h-4 w-4"
                        aria-describedby={nameId}
                        checked={defaultId === flow.id}
                        disabled={!canSelect}
                        onChange={() => setDefaultId(flow.id)}
                      />
                      {t("identityProofingFlows.selection.default")}
                    </label>
                  </Table.Cell>
                  <Table.Cell>
                    <label className="inline-flex items-center gap-2">
                      <input
                        type="checkbox"
                        className="h-4 w-4"
                        aria-describedby={nameId}
                        checked={allowed.has(flow.id) && canSelect}
                        disabled={!canSelect}
                        onChange={(event) =>
                          toggle(flow.id, event.target.checked)
                        }
                      />
                      {t("identityProofingFlows.selection.members")}
                    </label>
                  </Table.Cell>
                  <Table.Cell className="text-right">
                    <Link
                      to={flowPath(slug, flow.id)}
                      aria-label={t("identityProofingFlows.open", {
                        name: flow.name,
                      })}
                      className="text-ink hover:text-ink-soft inline-flex h-11 w-11 items-center justify-center"
                    >
                      <Icon name="chevron_right" size={OPEN_ICON_SIZE} />
                    </Link>
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </Card>
      {save.isError && (
        <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
      )}
      <div>
        <Button type="submit" loading={save.isPending} disabled={!dirty}>
          {t("identityProofingFlows.selection.save")}
        </Button>
      </div>
    </form>
  );
}

// See my data and Delete my data are not in the list: every org offers them,
// and they are set in Settings › Data and privacy, not edited as flows.
function DataRequestsNote({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <Card className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
      <div className="flex flex-col gap-1">
        <span className="text-ink text-[14px] font-semibold">
          {t("identityProofingFlows.dataRequests.title")}
        </span>
        <span className="text-ink-soft text-[13px]">
          {t("identityProofingFlows.dataRequests.hint")}
        </span>
      </div>
      <Link
        to={`/${slug}/settings?tab=privacy`}
        className="text-link text-[14px] font-semibold hover:underline"
      >
        {t("identityProofingFlows.dataRequests.link")}
      </Link>
    </Card>
  );
}

function flowPath(slug: string, flowId: string): string {
  return `/${slug}/identity-proofing/flows/${encodeURIComponent(flowId)}`;
}
