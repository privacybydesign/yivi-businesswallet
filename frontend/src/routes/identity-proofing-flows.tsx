import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useActivateFlowMutation,
  useProofingFlowVersionsQuery,
  useProofingFlowsQuery,
  useSetFlowSelectionMutation,
} from "../api/identity-proofing.queries";
import { isDataRequest } from "../api/identity-proofing";
import type { ProofingFlow } from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  CHECK_CHIP_AUTH,
  CHECK_FACE_MATCH,
  CHECK_LIVENESS,
  CHECK_PASSIVE_AUTH,
  assuranceLevelLabel,
  editedFlowSelection,
  isProofingStep,
  proofingErrorMessage,
} from "../lib/identity-proofing";
import { Button, Card, Tag, TopBar } from "../ui";
import { FlowHostedSettings } from "./flow-hosted-settings";
import { FlowEditor } from "./flow-editor";
import type { EditorMode } from "./flow-editor";

const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";

// The org admin's identity proofing flows: define flows at the proofing
// service with every setting it takes, edit one by saving a new version (active
// at once; sent requests keep the version they pinned), roll back to an earlier
// version, and choose which flows members may send a request on.
export default function IdentityProofingFlows(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const flows = useProofingFlowsQuery(slug, isAdmin);
  const [editor, setEditor] = useState<EditorMode | null>(null);

  return (
    <>
      <TopBar
        title={t("identityProofingFlows.title")}
        subtitle={t("identityProofingFlows.subtitle")}
        actions={
          isAdmin &&
          editor === null && (
            <Button icon="add" onClick={() => setEditor({ kind: "new" })}>
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
            {editor !== null && (
              <FlowEditor
                // A fresh editor per flow and version, seeded from it.
                key={
                  editor.kind === "new"
                    ? "new"
                    : `${editor.flow.id}@${editor.flow.version}`
                }
                slug={slug}
                mode={editor}
                onDone={() => setEditor(null)}
              />
            )}
            <FlowsCard
              slug={slug}
              flows={flows.data}
              onEdit={(flow) => setEditor({ kind: "edit", flow })}
            />
          </>
        )}
      </div>
    </>
  );
}

function FlowsCard({
  slug,
  flows,
  onEdit,
}: {
  slug: string;
  flows: ProofingFlow[];
  onEdit: (flow: ProofingFlow) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSetFlowSelectionMutation(slug);
  // Seeded once from the server; a flow created later starts unselected.
  const [allowed, setAllowed] = useState<ReadonlySet<string>>(
    () => new Set(flows.filter((f) => f.allowed).map((f) => f.id)),
  );
  const [defaultId, setDefaultId] = useState(
    () => flows.find((f) => f.default)?.id ?? "",
  );
  const [historyOf, setHistoryOf] = useState<string | null>(null);
  const [hostedOf, setHostedOf] = useState<string | null>(null);

  // A default the admin unticked falls to the first flow still ticked; a flow
  // that can no longer be ticked drops out.
  const selectable = flows.filter(
    (f) => f.completable && !isDataRequest(f.kind),
  );
  const { selection, dirty } = editedFlowSelection(
    selectable,
    allowed,
    defaultId,
    {
      flowIds: flows.filter((f) => f.allowed).map((f) => f.id),
      defaultFlowId: flows.find((f) => f.default)?.id ?? "",
    },
  );
  const effectiveDefault = selection.defaultFlowId;

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

  return (
    <Card className="p-6">
      <h2 className="font-display text-[16px] font-bold">
        {t("identityProofingFlows.selection.title")}
      </h2>
      <p className={`${HINT} mt-1`}>
        {t("identityProofingFlows.selection.hint")}
      </p>

      {flows.length === 0 ? (
        <p className="text-ink-soft mt-4 text-[13px]">
          {t("identityProofingFlows.selection.empty")}
        </p>
      ) : (
        <form className="mt-4 flex flex-col gap-4" onSubmit={submit} noValidate>
          <ul className="border-line divide-y rounded-lg border">
            {flows.map((flow) => {
              const checkboxId = `proofing-flow-allowed-${flow.id}`;
              const on = allowed.has(flow.id) && selectable.includes(flow);
              return (
                <li key={flow.id} className="flex flex-col gap-3 px-4 py-3">
                  <div className="flex flex-wrap items-start gap-x-6 gap-y-2">
                    <div className="flex min-w-0 flex-1 basis-64 items-start gap-2.5">
                      <input
                        id={checkboxId}
                        type="checkbox"
                        className="mt-0.5 h-4 w-4"
                        checked={on}
                        disabled={!flow.completable || isDataRequest(flow.kind)}
                        onChange={(event) =>
                          toggle(flow.id, event.target.checked)
                        }
                      />
                      <label
                        htmlFor={checkboxId}
                        className="flex min-w-0 flex-col gap-1"
                      >
                        <span className="flex flex-wrap items-center gap-2">
                          <span className="font-semibold">{flow.name}</span>
                          <Tag>
                            {t("identityProofingFlows.versionShort", {
                              version: flow.version,
                            })}
                          </Tag>
                          {flow.requiredAssuranceLevel && (
                            <Tag tone="blue">
                              {assuranceLevelLabel(
                                flow.requiredAssuranceLevel,
                                t,
                              )}
                            </Tag>
                          )}
                          {flow.kind && isDataRequest(flow.kind) && (
                            <Tag tone="amber">
                              {t(
                                `identityProofingFlows.kinds.${flow.kind}.title`,
                              )}
                            </Tag>
                          )}
                          {!flow.completable && (
                            <Tag tone="amber">
                              {t("identityProofingFlows.notCompletable")}
                            </Tag>
                          )}
                        </span>
                        <FlowSummary flow={flow} />
                      </label>
                    </div>
                    <label className="flex items-center gap-2 text-[13px]">
                      <input
                        type="radio"
                        name="proofing-default-flow"
                        className="h-4 w-4"
                        checked={on && effectiveDefault === flow.id}
                        disabled={!on}
                        onChange={() => setDefaultId(flow.id)}
                      />
                      {t("identityProofingFlows.selection.default")}
                    </label>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        type="button"
                        variant="secondary"
                        size="sm"
                        icon="edit"
                        onClick={() => onEdit(flow)}
                      >
                        {t("identityProofingFlows.edit")}
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        icon="time"
                        aria-expanded={historyOf === flow.id}
                        onClick={() =>
                          setHistoryOf((current) =>
                            current === flow.id ? null : flow.id,
                          )
                        }
                      >
                        {t("identityProofingFlows.versions.title")}
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        icon="settings"
                        aria-expanded={hostedOf === flow.id}
                        onClick={() =>
                          setHostedOf((current) =>
                            current === flow.id ? null : flow.id,
                          )
                        }
                      >
                        {t("identityProofingFlows.hosted.title")}
                      </Button>
                    </div>
                  </div>
                  {historyOf === flow.id && (
                    <VersionHistory slug={slug} flowId={flow.id} />
                  )}
                  {hostedOf === flow.id && (
                    <FlowHostedSettings slug={slug} flow={flow} />
                  )}
                </li>
              );
            })}
          </ul>
          {save.isError && (
            <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
          )}
          <div>
            <Button type="submit" loading={save.isPending} disabled={!dirty}>
              {t("identityProofingFlows.selection.save")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
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

// A flow version's steps and checks in a line, in words.
function FlowSummary({ flow }: { flow: ProofingFlow }): React.JSX.Element {
  const { t } = useTranslation();
  const steps = flow.steps.map((step) =>
    isProofingStep(step) ? t(`identityProofingFlows.steps.${step}`) : step,
  );
  // The wallet's own step, after the app's: uploaded in the browser.
  if (flow.diplomaMode === "required") {
    steps.push(t("identityProofingFlows.steps.diploma_upload"));
  }
  const checks = (flow.requiredChecks ?? []).map((check) => {
    const key = CHECK_LABELS[check];
    return key ? t(`identityProofingFlows.checks.${key}`) : check;
  });
  const parts = [steps.join(" → "), checks.join(", ")].filter(
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
    <div className="bg-surface-2 rounded-lg p-3">
      <p className={`${HINT} mb-2`}>
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
    </div>
  );
}
