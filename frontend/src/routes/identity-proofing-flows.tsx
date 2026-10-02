import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useActivateProofingFlowVersionMutation,
  useCreateProofingFlowMutation,
  useEditProofingFlowMutation,
  useProofingFlowVersionsQuery,
  useProofingFlowsQuery,
  useSetProofingFlowSelectionMutation,
  useSaveProofingFlowDiplomasMutation,
  useSaveProofingFlowKindMutation,
} from "../api/identity-proofing.queries";
import { FLOW_KINDS, isDataRequest } from "../api/identity-proofing";
import type {
  DiplomaMode,
  FlowKind,
  ProofingFlow,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  ASSURANCE_LEVELS,
  BSN_POLICIES,
  CHECK_CHIP_AUTH,
  CHECK_FACE_MATCH,
  CHECK_LIVENESS,
  CHECK_PASSIVE_AUTH,
  FACE_PROVIDERS,
  REQUESTED_ATTRIBUTES,
  attributeAvailable,
  draftFromFlow,
  draftSteps,
  editedFlowSelection,
  emptyFlowDraft,
  flowDraftError,
  flowSpecFromDraft,
  isProofingStep,
  levelRequirement,
  proofingErrorMessage,
  withAssuranceLevel,
} from "../lib/identity-proofing";
import type {
  EditableFlow,
  ProofingFlowDraft,
  Tristate,
} from "../lib/identity-proofing";
import { Button, Card, Input, Tag, TopBar } from "../ui";
import { FlowHostedSettings } from "./flow-hosted-settings";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const SECTION = "font-display text-[14px] font-bold";
// Mirrors the Input base so the selects read as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 py-2 text-[13.5px] leading-relaxed transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-10";

export type EditorMode = { kind: "new" } | { kind: "edit"; flow: EditableFlow };

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
  const save = useSetProofingFlowSelectionMutation(slug);
  // Seeded once from the server; a flow created later starts unselected.
  const [allowed, setAllowed] = useState<ReadonlySet<string>>(
    () => new Set(flows.filter((f) => f.allowed).map((f) => f.id)),
  );
  const [defaultId, setDefaultId] = useState(
    () => flows.find((f) => f.default)?.id ?? "",
  );
  const [historyOf, setHistoryOf] = useState<string | null>(null);
  const [hostedOf, setHostedOf] = useState<string | null>(null);

  // A default the admin unticked falls to the first flow still ticked.
  const { selection, dirty } = editedFlowSelection(flows, allowed, defaultId, {
    flowIds: flows.filter((f) => f.allowed).map((f) => f.id),
    defaultFlowId: flows.find((f) => f.default)?.id ?? "",
  });
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
              const on = allowed.has(flow.id);
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
                            <Tag tone="blue">{flow.requiredAssuranceLevel}</Tag>
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
  const activate = useActivateProofingFlowVersionMutation(slug, flowId);

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

// The flow editor, here and on a customer's Flows tab: onDone closes it (after
// a save or a cancel), onSaved gets the saved flow first.
export function FlowEditor({
  slug,
  mode,
  onDone,
  onSaved,
  note,
}: {
  slug: string;
  mode: EditorMode;
  onDone: () => void;
  onSaved?: (flow: ProofingFlow) => void;
  // Shown under the heading, e.g. that a shared flow changes for everyone.
  note?: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const editing = mode.kind === "edit" ? mode.flow : null;
  const create = useCreateProofingFlowMutation(slug);
  const edit = useEditProofingFlowMutation(slug, editing?.id ?? "");
  const save = editing ? edit : create;
  // Kept by the wallet, not the proofing service: saved after the flow.
  const saveDiplomas = useSaveProofingFlowDiplomasMutation(slug);
  const savedDiplomas: DiplomaMode = editing?.diplomaMode ?? "off";
  const [diplomaMode, setDiplomaMode] = useState<DiplomaMode>(savedDiplomas);
  const saveKind = useSaveProofingFlowKindMutation(slug);
  const savedKind: FlowKind = editing?.kind ?? "identity";
  const [flowKind, setFlowKind] = useState<FlowKind>(savedKind);
  const [draft, setDraft] = useState<ProofingFlowDraft>(() =>
    editing ? draftFromFlow(editing) : emptyFlowDraft(),
  );
  const [touched, setTouched] = useState(false);
  const invalid = flowDraftError(draft);
  const steps = draftSteps(draft);

  const required = levelRequirement(draft.assuranceLevel);

  function update(patch: Partial<ProofingFlowDraft>): void {
    setDraft((current) => ({ ...current, ...patch }));
  }

  function toggleAttribute(value: string, on: boolean): void {
    const next = new Set(draft.requestedAttributes);
    if (on) {
      next.add(value);
    } else {
      next.delete(value);
    }
    update({ requestedAttributes: next });
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (invalid !== null) {
      return;
    }
    save.mutate(flowSpecFromDraft(draft), {
      onSuccess: (flow) => {
        const finish = (): void => {
          onSaved?.(flow);
          onDone();
        };
        const kindThenFinish = (): void => {
          if (flowKind === savedKind) {
            finish();
            return;
          }
          saveKind.mutate(
            { flowId: flow.id, kind: flowKind },
            { onSuccess: finish },
          );
        };
        // A data request asks for no diplomas.
        const diplomas = isDataRequest(flowKind) ? "off" : diplomaMode;
        if (diplomas === savedDiplomas) {
          kindThenFinish();
          return;
        }
        saveDiplomas.mutate(
          { flowId: flow.id, mode: diplomas },
          { onSuccess: kindThenFinish },
        );
      },
    });
  }

  const fieldError = (field: typeof invalid): string | null =>
    touched && invalid === field
      ? t(`identityProofingFlows.new.errors.${field!}`)
      : null;

  return (
    <Card className="p-6">
      <form className="flex flex-col gap-6" onSubmit={submit} noValidate>
        <div>
          <h2 className="font-display text-[16px] font-bold">
            {editing
              ? t("identityProofingFlows.new.editTitle", {
                  name: editing.name,
                  version: editing.version + 1,
                })
              : t("identityProofingFlows.new.title")}
          </h2>
          <p className={`${HINT} mt-1`}>
            {editing
              ? t("identityProofingFlows.new.editHint", {
                  version: editing.version + 1,
                })
              : t("identityProofingFlows.new.hint")}
          </p>
          {note && <p className={`${HINT} mt-1 font-semibold`}>{note}</p>}
        </div>

        <Field
          id="proofing-flow-name"
          label={t("identityProofingFlows.new.name")}
          error={fieldError("name")}
        >
          <Input
            id="proofing-flow-name"
            placeholder={t("identityProofingFlows.new.namePlaceholder")}
            value={draft.name}
            onChange={(event) => update({ name: event.target.value })}
          />
        </Field>

        <Field
          id="proofing-flow-kind"
          label={t("identityProofingFlows.new.kind")}
          hint={t(`identityProofingFlows.kinds.${flowKind}.hint`)}
        >
          <select
            id="proofing-flow-kind"
            className={SELECT_CLASS}
            value={flowKind}
            onChange={(event) => setFlowKind(event.target.value as FlowKind)}
          >
            {FLOW_KINDS.map((kind) => (
              <option key={kind} value={kind}>
                {t(`identityProofingFlows.kinds.${kind}.title`)}
              </option>
            ))}
          </select>
          {saveKind.isError && (
            <p className={ERROR}>{proofingErrorMessage(saveKind.error, t)}</p>
          )}
        </Field>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>
            {t("identityProofingFlows.new.stepsTitle")}
          </legend>
          <Checkbox
            id="proofing-flow-step-document"
            checked={draft.documentAndChip}
            disabled={required.documentAndChip === true}
            label={t("identityProofingFlows.steps.document_capture")}
            hint={t("identityProofingFlows.new.documentCaptureHint")}
            onChange={(checked) => update({ documentAndChip: checked })}
          />
          <Checkbox
            id="proofing-flow-step-nfc"
            checked={draft.documentAndChip}
            disabled={required.documentAndChip === true}
            label={t("identityProofingFlows.steps.nfc_read")}
            hint={t("identityProofingFlows.new.nfcReadHint")}
            onChange={(checked) => update({ documentAndChip: checked })}
          />
          <Checkbox
            id="proofing-flow-step-document-photo"
            checked={draft.documentPhoto}
            label={t("identityProofingFlows.steps.document_photo")}
            hint={t("identityProofingFlows.new.documentPhotoHint")}
            onChange={(checked) => update({ documentPhoto: checked })}
          />
          <Checkbox
            id="proofing-flow-step-face"
            checked={draft.faceVerification}
            disabled={required.faceVerification === true}
            label={t("identityProofingFlows.steps.face_verification")}
            hint={t("identityProofingFlows.new.faceVerificationHint")}
            onChange={(checked) => update({ faceVerification: checked })}
          />
          <Checkbox
            id="proofing-flow-step-diplomas"
            checked={diplomaMode !== "off" && !isDataRequest(flowKind)}
            disabled={isDataRequest(flowKind)}
            label={t("identityProofingFlows.steps.diploma_upload")}
            hint={t("identityProofingFlows.new.diplomaUploadHint")}
            onChange={(checked) => setDiplomaMode(checked ? "required" : "off")}
          />
          {saveDiplomas.isError && (
            <p className={ERROR}>
              {proofingErrorMessage(saveDiplomas.error, t)}
            </p>
          )}
          <p className={HINT}>
            {steps.length > 0
              ? t("identityProofingFlows.new.stepsOrder", {
                  steps: [
                    ...steps
                      .filter(isProofingStep)
                      .map((step) => t(`identityProofingFlows.steps.${step}`)),
                    ...(diplomaMode === "off"
                      ? []
                      : [t("identityProofingFlows.steps.diploma_upload")]),
                  ].join(" → "),
                })
              : t("identityProofingFlows.new.errors.steps")}
          </p>
          {draft.faceVerification && (
            <p className={HINT}>
              {t("identityProofingFlows.new.faceLocationNative")}
            </p>
          )}
          {draft.faceVerification && (
            <Field
              id="proofing-flow-face-provider"
              label={t("identityProofingFlows.new.faceProvider")}
              hint={t("identityProofingFlows.new.faceProviderHint")}
            >
              <select
                id="proofing-flow-face-provider"
                className={SELECT_CLASS}
                value={draft.faceProvider}
                disabled={required.faceProvider !== undefined}
                onChange={(event) =>
                  update({
                    faceProvider: event.target
                      .value as ProofingFlowDraft["faceProvider"],
                  })
                }
              >
                {FACE_PROVIDERS.map((provider) => (
                  <option key={provider} value={provider}>
                    {t(`identityProofingFlows.faceProviders.${provider}`)}
                  </option>
                ))}
              </select>
            </Field>
          )}
          {draft.faceVerification && !draft.documentAndChip && (
            <p className="text-warning-fg text-[12.5px]">
              {t("identityProofingFlows.new.faceWithoutChip")}
            </p>
          )}
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>
            {t("identityProofingFlows.new.dataTitle")}
          </legend>
          <div className="grid gap-3 sm:grid-cols-2">
            {REQUESTED_ATTRIBUTES.map(({ value }) => {
              const available = attributeAvailable(draft, value);
              return (
                <Checkbox
                  key={value}
                  id={`proofing-flow-attr-${value}`}
                  checked={available && draft.requestedAttributes.has(value)}
                  disabled={!available}
                  label={t(`identityProofingFlows.attributes.${value}`)}
                  hint={t(`identityProofingFlows.attributeHints.${value}`)}
                  onChange={(checked) => toggleAttribute(value, checked)}
                />
              );
            })}
          </div>
          <p className={HINT}>{t("identityProofingFlows.new.dataHint")}</p>
        </fieldset>

        <fieldset className="flex flex-col gap-3">
          <legend className={SECTION}>
            {t("identityProofingFlows.new.checksTitle")}
          </legend>
          <div className="grid gap-3 sm:grid-cols-2">
            <Checkbox
              id="proofing-flow-check-passive"
              checked={draft.documentAndChip}
              disabled
              label={t("identityProofingFlows.checks.passiveAuth")}
              onChange={() => undefined}
            />
            <Checkbox
              id="proofing-flow-check-chip-auth"
              checked={draft.documentAndChip && draft.chipAuthentication}
              disabled={
                !draft.documentAndChip || required.chipAuthentication === true
              }
              label={t("identityProofingFlows.checks.chipAuth")}
              onChange={(checked) => update({ chipAuthentication: checked })}
            />
            <Checkbox
              id="proofing-flow-check-face-match"
              checked={draft.faceVerification}
              disabled
              label={t("identityProofingFlows.checks.faceMatch")}
              onChange={() => undefined}
            />
            <Checkbox
              id="proofing-flow-check-liveness"
              checked={draft.faceVerification && draft.liveness}
              disabled={!draft.faceVerification || required.liveness === true}
              label={t("identityProofingFlows.checks.liveness")}
              onChange={(checked) => update({ liveness: checked })}
            />
          </div>
          <p className={HINT}>{t("identityProofingFlows.new.checksHint")}</p>
          <Field
            id="proofing-flow-threshold"
            label={t("identityProofingFlows.new.faceMatchThreshold")}
            hint={t("identityProofingFlows.new.faceMatchThresholdHint")}
            error={fieldError("faceMatchThreshold")}
          >
            <Input
              id="proofing-flow-threshold"
              type="number"
              min={0}
              max={1}
              step={0.01}
              placeholder="0.8"
              disabled={!draft.faceVerification}
              value={draft.faceVerification ? draft.faceMatchThreshold : ""}
              onChange={(event) =>
                update({ faceMatchThreshold: event.target.value })
              }
            />
          </Field>
        </fieldset>

        <fieldset className="grid gap-4 sm:grid-cols-2">
          <legend className={`${SECTION} mb-3`}>
            {t("identityProofingFlows.new.documentsTitle")}
          </legend>
          <Field
            id="proofing-flow-doc-types"
            label={t("identityProofingFlows.new.documentTypes")}
            hint={t("identityProofingFlows.new.documentTypesHint")}
          >
            <Input
              id="proofing-flow-doc-types"
              placeholder="P, I"
              value={draft.acceptedDocumentTypes}
              onChange={(event) =>
                update({ acceptedDocumentTypes: event.target.value })
              }
            />
          </Field>
          <Field
            id="proofing-flow-countries"
            label={t("identityProofingFlows.new.issuingCountries")}
            hint={t("identityProofingFlows.new.issuingCountriesHint")}
          >
            <Input
              id="proofing-flow-countries"
              placeholder="NLD, BEL"
              value={draft.acceptedIssuingCountries}
              onChange={(event) =>
                update({ acceptedIssuingCountries: event.target.value })
              }
            />
          </Field>
        </fieldset>

        <fieldset className="grid gap-4 sm:grid-cols-2">
          <legend className={`${SECTION} mb-3`}>
            {t("identityProofingFlows.new.policyTitle")}
          </legend>
          <Field
            id="proofing-flow-assurance"
            label={t("identityProofingFlows.new.assuranceLevel")}
            hint={t(
              draft.assuranceLevel === ""
                ? "identityProofingFlows.new.assuranceLevelHint"
                : `identityProofingFlows.new.assuranceLevelNeeds.${draft.assuranceLevel}`,
            )}
          >
            <select
              id="proofing-flow-assurance"
              className={SELECT_CLASS}
              value={draft.assuranceLevel}
              onChange={(event) =>
                setDraft((current) =>
                  withAssuranceLevel(
                    current,
                    event.target.value as ProofingFlowDraft["assuranceLevel"],
                  ),
                )
              }
            >
              <option value="">{t("identityProofingFlows.new.none")}</option>
              {ASSURANCE_LEVELS.map((level) => (
                <option key={level} value={level}>
                  {t(`identityProofingFlows.assuranceLevels.${level}`)}
                </option>
              ))}
            </select>
          </Field>
          <Field
            id="proofing-flow-bsn"
            label={t("identityProofingFlows.new.bsnPolicy")}
          >
            <select
              id="proofing-flow-bsn"
              className={SELECT_CLASS}
              value={draft.bsnPolicy}
              onChange={(event) =>
                update({
                  bsnPolicy: event.target
                    .value as ProofingFlowDraft["bsnPolicy"],
                })
              }
            >
              <option value="">{t("identityProofingFlows.new.inherit")}</option>
              {BSN_POLICIES.map((policy) => (
                <option key={policy} value={policy}>
                  {policy}
                </option>
              ))}
            </select>
          </Field>
          <Field
            id="proofing-flow-retention"
            label={t("identityProofingFlows.new.retentionSeconds")}
            hint={t("identityProofingFlows.new.retentionSecondsHint")}
            error={fieldError("retentionSeconds")}
          >
            <Input
              id="proofing-flow-retention"
              type="number"
              min={0}
              placeholder="0"
              value={draft.retentionSeconds}
              onChange={(event) =>
                update({ retentionSeconds: event.target.value })
              }
            />
          </Field>
          <TristateField
            id="proofing-flow-blur-face"
            label={t("identityProofingFlows.new.blurFace")}
            value={draft.blurFace}
            onChange={(blurFace) => update({ blurFace })}
          />
          <TristateField
            id="proofing-flow-blur-bsn"
            label={t("identityProofingFlows.new.blurBsn")}
            value={draft.blurBsn}
            onChange={(blurBsn) => update({ blurBsn })}
          />
        </fieldset>

        {save.isError && (
          <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
        )}
        <div className="flex gap-2">
          <Button
            type="submit"
            icon={editing ? "edit" : "add"}
            loading={save.isPending}
          >
            {editing
              ? t("identityProofingFlows.new.saveVersion", {
                  version: editing.version + 1,
                })
              : t("identityProofingFlows.new.create")}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            {t("common.cancel")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

function Field({
  id,
  label,
  hint,
  error,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  error?: string | null;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className="flex max-w-md flex-col gap-1.5">
      <label htmlFor={id} className={LABEL}>
        {label}
      </label>
      {children}
      {error ? (
        <span className={ERROR}>{error}</span>
      ) : (
        hint && <span className={HINT}>{hint}</span>
      )}
    </div>
  );
}

function TristateField({
  id,
  label,
  value,
  onChange,
}: {
  id: string;
  label: string;
  value: Tristate;
  onChange: (value: Tristate) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Field id={id} label={label}>
      <select
        id={id}
        className={SELECT_CLASS}
        value={value}
        onChange={(event) => onChange(event.target.value as Tristate)}
      >
        <option value="">{t("identityProofingFlows.new.inherit")}</option>
        <option value="true">{t("identityProofingFlows.new.yes")}</option>
        <option value="false">{t("identityProofingFlows.new.no")}</option>
      </select>
    </Field>
  );
}

function Checkbox({
  id,
  checked,
  disabled = false,
  label,
  hint,
  onChange,
}: {
  id: string;
  checked: boolean;
  disabled?: boolean;
  label: string;
  hint?: string;
  onChange: (checked: boolean) => void;
}): React.JSX.Element {
  return (
    <div className="flex items-start gap-2.5">
      <input
        id={id}
        type="checkbox"
        className="mt-0.5 h-4 w-4"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      <label
        htmlFor={id}
        className={`flex flex-col ${disabled && !checked ? "opacity-50" : ""}`}
      >
        <span className="text-[13.5px] font-semibold">{label}</span>
        {hint && <span className={HINT}>{hint}</span>}
      </label>
    </div>
  );
}
