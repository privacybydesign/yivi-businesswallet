import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useActivateFlowMutation,
  useExternalRequestsQuery,
  useProofingFlowVersionsQuery,
  useProofingFlowsQuery,
} from "../api/identity-proofing.queries";
import type { ProofingFlow } from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  CHECK_CHIP_AUTH,
  CHECK_FACE_MATCH,
  CHECK_LIVENESS,
  CHECK_PASSIVE_AUTH,
  assuranceLevelLabel,
  flowStepLabels,
  listedCount,
  proofingErrorMessage,
  requestListCapped,
} from "../lib/identity-proofing";
import { Button, Card, Tag, TopBar } from "../ui";
import { FlowBrandingSettings } from "./flow-branding";
import { FlowHostedSettings } from "./flow-hosted-settings";
import { FlowEditor } from "./flow-editor";
import { FlowTabPanel, FlowTabs } from "./flow-tabs";
import { PeopleList } from "./proofing-people";
import { VerifyPerson } from "./proofing-send";

const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";

type TabKey = "people" | "versions";
const DEFAULT_TAB: TabKey = "people";

const TABS: TabKey[] = ["people", "versions"];
const TAB_PARAM = "tab";
const TAB_PREFIX = "flow-tab";
const TABPANEL_ID = "flow-tabpanel";

type EditTabKey = "check" | "branding" | "hosted";

const EDIT_TABS: EditTabKey[] = ["check", "branding", "hosted"];
const EDIT_TAB_PREFIX = "flow-edit-tab";

function editPanelId(tab: EditTabKey): string {
  return `flow-edit-panel-${tab}`;
}

// The label key of each check the flow editor offers; another check the
// service reports shows as it is.
const CHECK_LABELS: Record<
  string,
  "passiveAuth" | "chipAuth" | "faceMatch" | "liveness"
> = {
  [CHECK_PASSIVE_AUTH]: "passiveAuth",
  [CHECK_CHIP_AUTH]: "chipAuth",
  [CHECK_FACE_MATCH]: "faceMatch",
  [CHECK_LIVENESS]: "liveness",
};

// One flow of the org's: the people it identified and its versions, with its
// editor and the send form at the top. Admin only, like the flows list.
export default function IdentityProofingFlow(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, flowId } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const flows = useProofingFlowsQuery(slug, isAdmin);
  // A member's list of flows shares this cache entry, so it is not read for one.
  const flow = isAdmin ? flows.data?.find((f) => f.id === flowId) : undefined;

  if (flow) {
    return <FlowView slug={slug} flow={flow} />;
  }

  let body: React.JSX.Element;
  if (org.isPending || (isAdmin && flows.isPending)) {
    body = <p className="text-ink-soft">{t("common.loading")}</p>;
  } else if (!isAdmin) {
    body = (
      <p className="text-ink-soft">{t("identityProofingFlows.adminOnly")}</p>
    );
  } else if (flows.isError) {
    body = <p className={ERROR}>{proofingErrorMessage(flows.error, t)}</p>;
  } else {
    body = (
      <>
        <p className="text-ink-soft">
          {t("identityProofingFlows.page.notFound")}
        </p>
        <Link
          to={`/${slug}/identity-proofing/flows`}
          className="text-link font-semibold underline"
        >
          {t("identityProofingFlows.page.backToFlows")}
        </Link>
      </>
    );
  }

  return (
    <>
      <TopBar title={t("identityProofingFlows.title")} />
      <div className="flex flex-col gap-3 p-4 text-[14px] sm:p-8">{body}</div>
    </>
  );
}

function FlowView({
  slug,
  flow,
}: {
  slug: string;
  flow: ProofingFlow;
}): React.JSX.Element {
  const { t } = useTranslation();
  // The same list the People tab shows, narrowed to the flow by the backend.
  const people = useExternalRequestsQuery(slug, flow.id);
  const [searchParams, setSearchParams] = useSearchParams();
  const [editing, setEditing] = useState(false);
  const [editTab, setEditTab] = useState<EditTabKey>("check");
  const activeTab: TabKey =
    TABS.find((tab) => tab === searchParams.get(TAB_PARAM)) ?? DEFAULT_TAB;
  const headline = [
    ...(flow.default ? [t("identityProofingFlows.page.defaultFlow")] : []),
    t("identityProofingFlows.versionLong", { version: flow.version }),
    flow.requiredAssuranceLevel
      ? assuranceLevelLabel(flow.requiredAssuranceLevel, t)
      : t("identityProofing.flows.noAssurance"),
    flowStepLabels(flow, t).join(", "),
  ]
    .filter((part) => part !== "")
    .join(" · ");
  const count = people.data
    ? listedCount(people.data.length, requestListCapped(people.data), t)
    : undefined;

  function setTab(tab: TabKey): void {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (tab === DEFAULT_TAB) next.delete(TAB_PARAM);
        else next.set(TAB_PARAM, tab);
        // A session opened by a link belongs to the list it was opened on.
        next.delete("session");
        return next;
      },
      { replace: true },
    );
  }

  return (
    <>
      <TopBar
        title={flow.name}
        subtitle={headline}
        actions={
          <>
            {!editing && (
              <Button
                variant="secondary"
                onClick={() => {
                  setEditTab("check");
                  setEditing(true);
                }}
              >
                {t("identityProofingFlows.edit")}
              </Button>
            )}
            <VerifyPerson slug={slug} isAdmin flowId={flow.id} />
          </>
        }
      />
      {editing ? (
        <>
          <FlowTabs
            idPrefix={EDIT_TAB_PREFIX}
            label={t("identityProofingFlows.edit")}
            tabs={EDIT_TABS.map((tab) => ({
              key: tab,
              label: t(`identityProofingFlows.editTabs.${tab}`),
            }))}
            active={editTab}
            onSelect={setEditTab}
            panelId={editPanelId}
          />
          {/* All three stay mounted, so a draft survives a switch of tab. */}
          {EDIT_TABS.map((tab) => (
            <FlowTabPanel
              key={tab}
              idPrefix={EDIT_TAB_PREFIX}
              tab={tab}
              id={editPanelId(tab)}
              hidden={editTab !== tab}
              className="p-4 sm:p-8"
            >
              {tab === "check" ? (
                <FlowEditor
                  // A fresh editor per version, seeded from it.
                  key={`${flow.id}@${flow.version}`}
                  slug={slug}
                  mode={{ kind: "edit", flow }}
                  onDone={() => setEditing(false)}
                />
              ) : tab === "branding" ? (
                <FlowBrandingSettings slug={slug} flowId={flow.id} />
              ) : (
                <FlowHostedSettings slug={slug} flow={flow} />
              )}
            </FlowTabPanel>
          ))}
        </>
      ) : (
        <>
          <FlowTabs
            idPrefix={TAB_PREFIX}
            label={t("identityProofingFlows.page.tabsLabel")}
            tabs={TABS.map((tab) => ({
              key: tab,
              label:
                tab === "people" ? (
                  <>
                    {t("identityProofingFlows.page.people")}
                    {count !== undefined && (
                      <span className="text-muted font-normal">{count}</span>
                    )}
                  </>
                ) : (
                  t("identityProofingFlows.versions.title")
                ),
            }))}
            active={activeTab}
            onSelect={setTab}
            panelId={() => TABPANEL_ID}
          />
          <FlowTabPanel
            idPrefix={TAB_PREFIX}
            tab={activeTab}
            id={TABPANEL_ID}
            className="flex flex-col gap-4 p-4 sm:p-8"
          >
            {activeTab === "people" ? (
              <PeopleList slug={slug} isAdmin flow={flow} />
            ) : (
              <Card className="p-5">
                <VersionHistory slug={slug} flowId={flow.id} />
              </Card>
            )}
          </FlowTabPanel>
        </>
      )}
    </>
  );
}

// A flow version's steps and checks in a line, in words.
function FlowSummary({ flow }: { flow: ProofingFlow }): React.JSX.Element {
  const { t } = useTranslation();
  const checks = (flow.requiredChecks ?? []).map((check) => {
    const key = CHECK_LABELS[check];
    return key ? t(`identityProofingFlows.checks.${key}`) : check;
  });
  const parts = [flowStepLabels(flow, t).join(" → "), checks.join(", ")].filter(
    (part) => part !== "",
  );
  return (
    <span className="text-ink-soft text-[12px]">{parts.join("  ·  ")}</span>
  );
}

function VersionHistory({
  slug,
  flowId,
}: {
  slug: string;
  flowId: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const versions = useProofingFlowVersionsQuery(slug, flowId, true);
  const activate = useActivateFlowMutation(slug, flowId);

  if (versions.isPending) {
    return <p className={HINT}>{t("common.loading")}</p>;
  }
  if (versions.isError) {
    return <p className={ERROR}>{proofingErrorMessage(versions.error, t)}</p>;
  }
  // Newest first reads as a history.
  const newestFirst = [...versions.data].reverse();
  return (
    <>
      <p className={`${HINT} mb-3`}>
        {t("identityProofingFlows.versions.hint")}
      </p>
      <ul className="flex flex-col gap-2">
        {newestFirst.map((version) => (
          <li
            key={version.version}
            className="flex flex-wrap items-center gap-x-4 gap-y-1"
          >
            <span className="font-semibold">
              {t("identityProofingFlows.versionShort", {
                version: version.version,
              })}
            </span>
            <span className="text-[13px]">{version.name}</span>
            <span className={HINT}>{formatWhen(version.createdAt)}</span>
            <FlowSummary flow={version} />
            {version.active ? (
              <Tag tone="green">
                {t("identityProofingFlows.versions.active")}
              </Tag>
            ) : (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                loading={
                  activate.isPending && activate.variables === version.version
                }
                onClick={() => activate.mutate(version.version)}
              >
                {t("identityProofingFlows.versions.activate")}
              </Button>
            )}
          </li>
        ))}
      </ul>
      {activate.isError && (
        <p className={`${ERROR} mt-2`}>
          {proofingErrorMessage(activate.error, t)}
        </p>
      )}
    </>
  );
}
