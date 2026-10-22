import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { FLOW_KINDS, isDataRequest } from "../api/identity-proofing";
import type {
  AddressMode,
  DiplomaMode,
  FlowKind,
  ProofingSettings,
} from "../api/identity-proofing";
import {
  ATTRIBUTE_DOCUMENT,
  BSN_POLICIES,
  DOCUMENT_KINDS,
  FOLLOW_ORG,
  REQUESTED_ATTRIBUTES,
  SESSION_TTL_OPTIONS_SECONDS,
  assuranceLevelLabel,
  attributeAvailable,
  lockedByLevel,
  retentionChoices,
  ttlMinutes,
  wholeDays,
} from "../lib/identity-proofing";
import type {
  FlowDraftError,
  LevelRequirement,
  ProofingFlowDraft,
} from "../lib/identity-proofing";
import {
  issuingCountryChoices,
  issuingCountryName,
} from "../lib/issuing-countries";
import {
  ChevronSelect,
  ERROR,
  HINT,
  OptionRow,
  Section,
} from "./flow-editor-parts";
import type { OptionLock } from "./flow-editor-parts";

const LABEL = "text-[14px] font-semibold";
const RADIO = "h-[18px] w-[18px] flex-none accent-ink";

interface AdvancedSettingsProps {
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
  // The org's own settings, for what a flow's choice defaults to.
  settings: ProofingSettings | undefined;
  kind: FlowKind;
  onKind: (kind: FlowKind) => void;
  kindError: string | null;
  diplomaMode: DiplomaMode;
  onDiploma: (mode: DiplomaMode) => void;
  diplomaError: string | null;
  addressMode: AddressMode;
  onAddress: (mode: AddressMode) => void;
  addressError: string | null;
  // Whether the flow must read the name and date of birth: a data request, or
  // required diplomas.
  requiresIdentity: boolean;
  // The field the editor refused to save, once it was tried.
  error: FlowDraftError | null;
}

// Everything under Advanced settings, grouped by the question it answers.
export function AdvancedSettings(
  props: AdvancedSettingsProps,
): React.JSX.Element {
  const { draft, update, settings, error } = props;

  return (
    <>
      <StepsSection {...props} />
      <DocumentsSection draft={draft} update={update} error={error} />
      <ReceiveSection
        draft={draft}
        update={update}
        requiresIdentity={props.requiresIdentity}
        error={error}
      />
      <ChecksSection draft={draft} update={update} />
      <PrivacySection draft={draft} update={update} settings={settings} />
    </>
  );
}

// The set with value in it if it was out, out if it was in.
function toggled<T>(set: ReadonlySet<T>, value: T): Set<T> {
  const next = new Set(set);
  if (!next.delete(value)) {
    next.add(value);
  }
  return next;
}

function flowKindOf(value: string): FlowKind {
  return FLOW_KINDS.find((kind) => kind === value) ?? "identity";
}

// What the draft's level says about a setting it needs on: the lock note and
// the tooltip, or none when the level leaves it free.
function useLevelLock(
  draft: ProofingFlowDraft,
): (setting: keyof LevelRequirement) => OptionLock | undefined {
  const { t } = useTranslation();
  const level = assuranceLevelLabel(draft.assuranceLevel, t);
  return (setting) =>
    lockedByLevel(draft, setting)
      ? {
          note: t("identityProofingFlows.editor.locks.required", { level }),
          tooltip: t("identityProofingFlows.editor.locks.mandatory", { level }),
        }
      : undefined;
}

function StepsSection({
  draft,
  update,
  settings,
  kind,
  onKind,
  kindError,
  diplomaMode,
  onDiploma,
  diplomaError,
  addressMode,
  onAddress,
  addressError,
  error,
}: AdvancedSettingsProps): React.JSX.Element {
  const { t } = useTranslation();
  const levelLock = useLevelLock(draft);
  const dataRequest = isDataRequest(kind);
  const orgTtl = settings?.sessionTtlSeconds;

  return (
    <Section
      title={t("identityProofingFlows.editor.steps.title")}
      hint={t("identityProofingFlows.editor.steps.hint")}
    >
      <div className="flex max-w-md flex-col gap-1.5">
        <label htmlFor="flow-kind" className={LABEL}>
          {t("identityProofingFlows.new.kind")}
        </label>
        <ChevronSelect
          id="flow-kind"
          value={kind}
          onChange={(value) => onKind(flowKindOf(value))}
        >
          {FLOW_KINDS.map((option) => (
            <option key={option} value={option}>
              {t(`identityProofingFlows.kinds.${option}.title`)}
            </option>
          ))}
        </ChevronSelect>
        <span className={HINT}>
          {t(`identityProofingFlows.kinds.${kind}.hint`)}
        </span>
        {kindError && (
          <p role="alert" className={ERROR}>
            {kindError}
          </p>
        )}
      </div>
      <OptionRow
        id="flow-step-scan"
        checked={draft.documentAndChip}
        label={t("identityProofingFlows.editor.steps.scanAndRead")}
        hint={t("identityProofingFlows.editor.steps.scanAndReadHint")}
        lock={levelLock("documentAndChip")}
        onChange={() => update({ documentAndChip: !draft.documentAndChip })}
      />
      <OptionRow
        id="flow-step-photo"
        checked={draft.documentPhoto}
        label={t("identityProofingFlows.steps.document_photo")}
        hint={t("identityProofingFlows.editor.steps.photoHint")}
        onChange={() => update({ documentPhoto: !draft.documentPhoto })}
      />
      <OptionRow
        id="flow-step-face"
        checked={draft.faceVerification}
        label={t("identityProofingFlows.steps.face_verification")}
        hint={t("identityProofingFlows.editor.steps.faceHint")}
        lock={levelLock("faceVerification")}
        onChange={() => update({ faceVerification: !draft.faceVerification })}
      />
      {draft.faceVerification && !draft.documentAndChip && (
        <p className="text-warning-fg text-[12.5px]">
          {t("identityProofingFlows.editor.steps.faceWithoutChip")}
        </p>
      )}
      {error === "steps" && (
        <p role="alert" className={ERROR}>
          {t("identityProofingFlows.new.errors.steps")}
        </p>
      )}
      <OptionRow
        id="flow-step-address"
        checked={addressMode !== "off" && !dataRequest}
        disabled={dataRequest}
        label={t("identityProofingFlows.steps.address")}
        hint={t("identityProofingFlows.editor.steps.addressHint")}
        onChange={() => onAddress(addressMode === "off" ? "required" : "off")}
      />
      {addressError && (
        <p role="alert" className={ERROR}>
          {addressError}
        </p>
      )}
      <OptionRow
        id="flow-step-diplomas"
        checked={diplomaMode !== "off" && !dataRequest}
        disabled={dataRequest}
        label={t("identityProofingFlows.steps.diploma_upload")}
        hint={t("identityProofingFlows.editor.steps.diplomaHint")}
        onChange={() => onDiploma(diplomaMode === "off" ? "required" : "off")}
      />
      {diplomaError && (
        <p role="alert" className={ERROR}>
          {diplomaError}
        </p>
      )}
      <div className="flex max-w-md flex-col gap-1.5">
        <label htmlFor="flow-lifetime" className={LABEL}>
          {t("identityProofingFlows.editor.steps.lifetime")}
        </label>
        <ChevronSelect
          id="flow-lifetime"
          value={String(draft.sessionTtlSeconds)}
          onChange={(value) => update({ sessionTtlSeconds: Number(value) })}
        >
          <option value={FOLLOW_ORG}>
            {orgTtl === undefined
              ? t("identityProofingFlows.editor.orgDefault")
              : t("identityProofingFlows.editor.steps.lifetimeDefault", {
                  minutes: ttlMinutes(orgTtl),
                })}
          </option>
          {SESSION_TTL_OPTIONS_SECONDS.map((seconds) => (
            <option key={seconds} value={seconds}>
              {t("identityProofingFlows.editor.steps.minutes", {
                count: ttlMinutes(seconds),
              })}
            </option>
          ))}
        </ChevronSelect>
        <span className={HINT}>
          {t("identityProofingFlows.editor.steps.lifetimeHint")}
        </span>
      </div>
    </Section>
  );
}

function DocumentsSection({
  draft,
  update,
  error,
}: {
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
  error: FlowDraftError | null;
}): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const choices = useMemo(
    () => issuingCountryChoices(i18n.language, t),
    [i18n.language, t],
  );
  const open = choices.filter(
    (choice) => !draft.issuingCountries.includes(choice.code),
  );

  return (
    <Section
      title={t("identityProofingFlows.editor.documents.title")}
      hint={t("identityProofingFlows.editor.documents.hint")}
    >
      <fieldset className="flex flex-col gap-2.5">
        <legend className={`${LABEL} mb-1`}>
          {t("identityProofingFlows.editor.documents.types")}
        </legend>
        {DOCUMENT_KINDS.map((kind) => (
          <OptionRow
            key={kind}
            id={`flow-doc-${kind}`}
            checked={draft.documentKinds.has(kind)}
            label={t(`identityProofingFlows.editor.documents.${kind}`)}
            onChange={() =>
              update({ documentKinds: toggled(draft.documentKinds, kind) })
            }
          />
        ))}
        {error === "documentTypes" && (
          <p role="alert" className={ERROR}>
            {t("identityProofingFlows.new.errors.documentTypes")}
          </p>
        )}
      </fieldset>
      <fieldset className="flex flex-col gap-2.5">
        <legend className={`${LABEL} mb-1`}>
          {t("identityProofingFlows.editor.documents.countries")}
        </legend>
        <label className="flex cursor-pointer items-center gap-2.5">
          <input
            type="radio"
            name="flow-country-scope"
            className={RADIO}
            checked={draft.countryScope === "any"}
            onChange={() => update({ countryScope: "any" })}
          />
          {t("identityProofingFlows.editor.documents.any")}
        </label>
        <label className="flex cursor-pointer items-center gap-2.5">
          <input
            type="radio"
            name="flow-country-scope"
            className={RADIO}
            checked={draft.countryScope === "only"}
            onChange={() => update({ countryScope: "only" })}
          />
          {t("identityProofingFlows.editor.documents.only")}
        </label>
        {draft.countryScope === "only" && (
          <div className="ml-7 flex max-w-md flex-col gap-2">
            {draft.issuingCountries.length > 0 && (
              <ul className="border-line rounded-yivi divide-line divide-y border">
                {draft.issuingCountries.map((code) => {
                  const name = issuingCountryName(code, i18n.language, t);
                  return (
                    <li
                      key={code}
                      className="flex items-center justify-between py-1.5 pr-1.5 pl-3.5"
                    >
                      {name}
                      <button
                        type="button"
                        aria-label={t(
                          "identityProofingFlows.editor.documents.remove",
                          { country: name },
                        )}
                        className="text-ink hover:bg-surface-3 rounded-yivi h-8 cursor-pointer px-2.5 font-semibold"
                        onClick={() =>
                          update({
                            issuingCountries: draft.issuingCountries.filter(
                              (kept) => kept !== code,
                            ),
                          })
                        }
                      >
                        {t(
                          "identityProofingFlows.editor.documents.removeLabel",
                        )}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
            <label htmlFor="flow-country-add" className="sr-only">
              {t("identityProofingFlows.editor.documents.add")}
            </label>
            <ChevronSelect
              id="flow-country-add"
              value=""
              onChange={(code) =>
                code !== "" &&
                update({ issuingCountries: [...draft.issuingCountries, code] })
              }
            >
              <option value="">
                {t("identityProofingFlows.editor.documents.addPlaceholder")}
              </option>
              {open.map((choice) => (
                <option key={choice.code} value={choice.code}>
                  {choice.name}
                </option>
              ))}
            </ChevronSelect>
            <p className={HINT}>
              {t("identityProofingFlows.editor.documents.addHint")}
            </p>
            {error === "issuingCountries" && (
              <p role="alert" className={ERROR}>
                {t("identityProofingFlows.new.errors.issuingCountries")}
              </p>
            )}
          </div>
        )}
      </fieldset>
    </Section>
  );
}

function ReceiveSection({
  draft,
  update,
  requiresIdentity,
  error,
}: {
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
  requiresIdentity: boolean;
  error: FlowDraftError | null;
}): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <Section
      title={t("identityProofingFlows.editor.receive.title")}
      hint={t("identityProofingFlows.editor.receive.hint")}
    >
      {REQUESTED_ATTRIBUTES.map(({ value }) => {
        const available = attributeAvailable(draft, value);
        // Once ticked, Document data stays on: an older version without it can
        // still tick it.
        const locked =
          requiresIdentity &&
          value === ATTRIBUTE_DOCUMENT &&
          draft.requestedAttributes.has(value);
        return (
          <OptionRow
            key={value}
            id={`flow-attr-${value}`}
            checked={available && draft.requestedAttributes.has(value)}
            disabled={!available || locked}
            label={t(`identityProofingFlows.attributes.${value}`)}
            hint={t(`identityProofingFlows.attributeHints.${value}`)}
            onChange={() =>
              update({
                requestedAttributes: toggled(draft.requestedAttributes, value),
              })
            }
          />
        );
      })}
      {requiresIdentity && (
        <p className={HINT}>
          {t("identityProofingFlows.editor.receive.identityLocked")}
        </p>
      )}
      {error === "identity" && (
        <p role="alert" className={ERROR}>
          {t("identityProofingFlows.new.errors.identity")}
        </p>
      )}
    </Section>
  );
}

function ChecksSection({
  draft,
  update,
}: {
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const levelLock = useLevelLock(draft);
  // A check its step always runs is locked on with the step, whatever the level.
  const passiveLock =
    levelLock("documentAndChip") ??
    (draft.documentAndChip
      ? { note: t("identityProofingFlows.editor.locks.passiveAuth") }
      : undefined);
  const faceMatchLock =
    levelLock("faceVerification") ??
    (draft.faceVerification
      ? { note: t("identityProofingFlows.editor.locks.faceMatch") }
      : undefined);

  return (
    <Section
      title={t("identityProofingFlows.editor.checks.title")}
      hint={t("identityProofingFlows.editor.checks.hint")}
    >
      <OptionRow
        id="flow-check-passive"
        checked={draft.documentAndChip}
        disabled
        label={t("identityProofingFlows.checks.passiveAuth")}
        lock={passiveLock}
      />
      <OptionRow
        id="flow-check-chip-auth"
        checked={draft.documentAndChip && draft.chipAuthentication}
        disabled={!draft.documentAndChip}
        label={t("identityProofingFlows.checks.chipAuth")}
        lock={levelLock("chipAuthentication")}
        onChange={() =>
          update({ chipAuthentication: !draft.chipAuthentication })
        }
      />
      <OptionRow
        id="flow-check-face-match"
        checked={draft.faceVerification}
        disabled
        label={t("identityProofingFlows.checks.faceMatch")}
        lock={faceMatchLock}
      />
      <OptionRow
        id="flow-check-liveness"
        checked={draft.faceVerification && draft.liveness}
        disabled={!draft.faceVerification}
        label={t("identityProofingFlows.checks.liveness")}
        lock={levelLock("liveness")}
        onChange={() => update({ liveness: !draft.liveness })}
      />
      {/* The engine refuses an expired document on every flow (DOC_EXPIRED);
          nothing turns it off. */}
      <OptionRow
        id="flow-check-expired"
        checked
        disabled
        label={t("identityProofingFlows.editor.checks.expired")}
        lock={{ note: t("identityProofingFlows.editor.locks.everyFlow") }}
      />
    </Section>
  );
}

function PrivacySection({
  draft,
  update,
  settings,
}: {
  draft: ProofingFlowDraft;
  update: (patch: Partial<ProofingFlowDraft>) => void;
  settings: ProofingSettings | undefined;
}): React.JSX.Element {
  const { t } = useTranslation();
  const orgDays = settings?.dataRetentionDays;

  function retentionLabel(seconds: number): string {
    if (seconds === FOLLOW_ORG) {
      return orgDays === undefined
        ? t("identityProofingFlows.editor.orgDefault")
        : t("identityProofingFlows.editor.privacy.retentionDefault", {
            days: orgDays,
          });
    }
    const days = wholeDays(seconds);
    return days === undefined
      ? t("identityProofingFlows.editor.privacy.retentionSeconds", {
          count: seconds,
        })
      : t("identityProofing.settings.days", { count: days });
  }

  return (
    <Section
      title={t("identityProofingFlows.editor.privacy.title")}
      hint={t("identityProofingFlows.editor.privacy.hint")}
    >
      {/* Two columns where 260 px each fit, else one as wide as there is. */}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(260px,100%),1fr))] gap-x-6 gap-y-5">
        <div className="flex flex-col gap-1.5">
          <label htmlFor="flow-retention" className={LABEL}>
            {t("identityProofingFlows.editor.privacy.retention")}
          </label>
          <ChevronSelect
            id="flow-retention"
            value={String(draft.retentionSeconds)}
            onChange={(value) => update({ retentionSeconds: Number(value) })}
          >
            {retentionChoices(draft.retentionSeconds).map((seconds) => (
              <option key={seconds} value={seconds}>
                {retentionLabel(seconds)}
              </option>
            ))}
          </ChevronSelect>
          <span className={HINT}>
            {t("identityProofingFlows.editor.privacy.retentionHint")}
          </span>
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="flow-bsn" className={LABEL}>
            {t("identityProofingFlows.editor.privacy.bsn")}
          </label>
          <ChevronSelect
            id="flow-bsn"
            value={draft.bsnPolicy}
            onChange={(value) =>
              update({
                bsnPolicy:
                  BSN_POLICIES.find((policy) => policy === value) ??
                  draft.bsnPolicy,
              })
            }
          >
            {BSN_POLICIES.map((policy) => (
              <option key={policy} value={policy}>
                {t(`identityProofingFlows.bsnPolicies.${policy}`)}
              </option>
            ))}
          </ChevronSelect>
          <span className={HINT}>
            {t("identityProofingFlows.editor.privacy.bsnHint")}
          </span>
        </div>
      </div>
      <OptionRow
        id="flow-black-out-bsn"
        checked={draft.blackOutBsn}
        label={t("identityProofingFlows.editor.privacy.blackOutBsn")}
        onChange={() => update({ blackOutBsn: !draft.blackOutBsn })}
      />
      <OptionRow
        id="flow-keep-face-photos"
        checked={draft.keepFacePhotos}
        label={t("identityProofingFlows.editor.privacy.keepFacePhotos")}
        hint={t("identityProofingFlows.editor.privacy.keepFacePhotosHint")}
        onChange={() => update({ keepFacePhotos: !draft.keepFacePhotos })}
      />
    </Section>
  );
}
