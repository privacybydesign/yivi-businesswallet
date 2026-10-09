import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateProofingFlowMutation,
  useEditProofingFlowMutation,
  useSaveFlowDiplomasMutation,
  useSaveFlowKindMutation,
} from "../api/identity-proofing.queries";
import { FLOW_KINDS, isDataRequest } from "../api/identity-proofing";
import type {
  DiplomaMode,
  FlowKind,
  ProofingFlow,
} from "../api/identity-proofing";
import {
  ASSURANCE_LEVELS,
  BSN_POLICIES,
  FACE_PROVIDERS,
  REQUESTED_ATTRIBUTES,
  attributeAvailable,
  draftFromFlow,
  draftSteps,
  emptyFlowDraft,
  flowDraftError,
  flowSpecFromDraft,
  isProofingStep,
  proofingErrorMessage,
} from "../lib/identity-proofing";
import type {
  EditableFlow,
  ProofingFlowDraft,
  Tristate,
} from "../lib/identity-proofing";
import { Button, Card, Input } from "../ui";

const LABEL = "text-ink-soft text-[12px] font-semibold";
const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const SECTION = "font-display text-[14px] font-bold";
// Mirrors the Input base so the selects read as the same field.
const SELECT_CLASS =
  "rounded-yivi border-line-strong bg-surface text-ink w-full border px-3 py-2 text-[13.5px] leading-relaxed transition-colors outline-none focus:border-ink focus:ring-ink/10 focus:ring-3 h-10";

export type EditorMode = { kind: "new" } | { kind: "edit"; flow: EditableFlow };

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
  // The flow this editor already saved, with the spec it was saved with. When
  // a follow-up save (diplomas, kind) fails, a retry must not save the flow
  // again: a create would make a second flow, an edit another version.
  const [saved, setSaved] = useState<{
    flow: ProofingFlow;
    spec: string;
  } | null>(null);
  const create = useCreateProofingFlowMutation(slug);
  const edit = useEditProofingFlowMutation(
    slug,
    saved?.flow.id ?? editing?.id ?? "",
  );
  const save = editing || saved ? edit : create;
  // Kept by the wallet, not the proofing service: saved after the flow.
  const saveDiplomas = useSaveFlowDiplomasMutation(slug);
  const savedDiplomas: DiplomaMode = editing?.diplomaMode ?? "off";
  const [diplomaMode, setDiplomaMode] = useState<DiplomaMode>(savedDiplomas);
  const saveKind = useSaveFlowKindMutation(slug);
  const savedKind: FlowKind = editing?.kind ?? "identity";
  const [flowKind, setFlowKind] = useState<FlowKind>(savedKind);
  const [draft, setDraft] = useState<ProofingFlowDraft>(() =>
    editing ? draftFromFlow(editing) : emptyFlowDraft(),
  );
  const [touched, setTouched] = useState(false);
  const invalid = flowDraftError(draft);
  const steps = draftSteps(draft);

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
    const spec = flowSpecFromDraft(draft);
    const specKey = JSON.stringify(spec);
    const followUps = (flow: ProofingFlow): void => {
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
    };
    if (saved !== null && saved.spec === specKey) {
      followUps(saved.flow);
      return;
    }
    save.mutate(spec, {
      onSuccess: (flow) => {
        setSaved({ flow, spec: specKey });
        followUps(flow);
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
            label={t("identityProofingFlows.steps.document_capture")}
            hint={t("identityProofingFlows.new.documentCaptureHint")}
            onChange={(checked) => update({ documentAndChip: checked })}
          />
          <Checkbox
            id="proofing-flow-step-nfc"
            checked={draft.documentAndChip}
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
              disabled={!draft.documentAndChip}
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
              disabled={!draft.faceVerification}
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
            error={fieldError("issuingCountries")}
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
            error={fieldError("assuranceLevel")}
          >
            <select
              id="proofing-flow-assurance"
              className={SELECT_CLASS}
              value={draft.assuranceLevel}
              onChange={(event) =>
                update({
                  assuranceLevel: event.target
                    .value as ProofingFlowDraft["assuranceLevel"],
                })
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
                  {t(`identityProofingFlows.bsnPolicies.${policy}`)}
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
