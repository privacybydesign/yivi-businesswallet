import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useCreateProofingFlowMutation,
  useEditProofingFlowMutation,
  useProofingSettingsQuery,
  useSaveFlowAddressMutation,
  useSaveFlowDiplomasMutation,
} from "../api/identity-proofing.queries";
import type {
  AddressMode,
  DiplomaMode,
  ProofingFlow,
} from "../api/identity-proofing";
import {
  ASSURANCE_LEVELS,
  draftFromFlow,
  emptyFlowDraft,
  flowDraftError,
  flowSpecFromDraft,
  identityRequired,
  proofingErrorMessage,
  withAssuranceLevel,
  withIdentity,
} from "../lib/identity-proofing";
import type {
  AssuranceLevel,
  EditableFlow,
  FlowDraftError,
  ProofingFlowDraft,
} from "../lib/identity-proofing";
import { Button, Card, Icon, Input } from "../ui";
import { AdvancedSettings } from "./flow-editor-advanced";
import { ERROR, HINT } from "./flow-editor-parts";

const ADVANCED_ID = "flow-advanced";
const NAME_ID = "proofing-flow-name";
const NAME_HINT_ID = "proofing-flow-name-hint";
const NAME_ERROR_ID = "proofing-flow-name-error";
const CHEVRON_SIZE = 18;
const INFO_SIZE = 16;

// The fields of the draft that sit under Advanced settings: an error in one of
// them opens it.
const ADVANCED_ERRORS: ReadonlySet<FlowDraftError> = new Set([
  "steps",
  "identity",
  "documentTypes",
  "issuingCountries",
  "ageOver",
]);

// The levels the editor offers, as the draft holds them: "" is none.
const LEVEL_CHOICES = ["none", ...ASSURANCE_LEVELS] as const;
type LevelChoice = (typeof LEVEL_CHOICES)[number];

function levelOf(choice: LevelChoice): AssuranceLevel | "" {
  return choice === "none" ? "" : choice;
}

export type EditorMode = { kind: "new" } | { kind: "edit"; flow: EditableFlow };

// The flow editor: onDone closes it (after a save or a cancel), onSaved gets
// the saved flow first.
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
  // a follow-up save (diplomas, address) fails, a retry must not save
  // the flow again: a create would make a second flow, an edit another version.
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
  const settings = useProofingSettingsQuery(slug);
  // Kept by the wallet, not the proofing service: saved after the flow.
  const saveDiplomas = useSaveFlowDiplomasMutation(slug);
  const savedDiplomas: DiplomaMode = editing?.diplomaMode ?? "off";
  const [diplomaMode, setDiplomaMode] = useState<DiplomaMode>(savedDiplomas);
  const saveAddress = useSaveFlowAddressMutation(slug);
  const savedAddress: AddressMode = editing?.addressMode ?? "off";
  const [addressMode, setAddressMode] = useState<AddressMode>(savedAddress);
  const [draft, setDraft] = useState<ProofingFlowDraft>(() =>
    editing ? draftFromFlow(editing) : emptyFlowDraft(),
  );
  const [touched, setTouched] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const address = addressMode;
  const diplomas = diplomaMode;
  const requiresIdentity = identityRequired(diplomas);
  const invalid = flowDraftError(draft, requiresIdentity);
  const shown = touched ? invalid : null;
  const version = (editing?.version ?? 0) + 1;

  function update(patch: Partial<ProofingFlowDraft>): void {
    setDraft((current) => ({ ...current, ...patch }));
  }

  // Choosing required diplomas ticks Document data, which then stays ticked
  // (identityRequired), and unticks the age check, which withholds the date
  // of birth they need.
  function chooseDiplomas(mode: DiplomaMode): void {
    setDiplomaMode(mode);
    if (identityRequired(mode)) {
      setDraft(withIdentity);
    }
  }

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (invalid !== null) {
      if (ADVANCED_ERRORS.has(invalid)) {
        setAdvancedOpen(true);
      }
      return;
    }
    const spec = flowSpecFromDraft(draft);
    const specKey = JSON.stringify(spec);
    const finish = (flow: ProofingFlow): void => {
      onSaved?.(flow);
      onDone();
    };
    // The diploma mode, then the address, each only when it changed.
    const saveSettings = (flowId: string, then: () => void): void => {
      const addressThen = (): void => {
        if (address === savedAddress) {
          then();
          return;
        }
        saveAddress.mutate({ flowId, mode: address }, { onSuccess: then });
      };
      if (diplomas === savedDiplomas) {
        addressThen();
        return;
      }
      saveDiplomas.mutate(
        { flowId, mode: diplomas },
        { onSuccess: addressThen },
      );
    };
    const saveSpec = (then: (flow: ProofingFlow) => void): void => {
      if (saved !== null && saved.spec === specKey) {
        then(saved.flow);
        return;
      }
      save.mutate(spec, {
        onSuccess: (flow) => {
          setSaved({ flow, spec: specKey });
          then(flow);
        },
      });
    };
    // The service holds a required-diplomas flow to reading the name and date
    // of birth, in each version and setting. Settings that
    // lift that go before a version that may drop it; otherwise the version
    // goes first, and the settings that need it check the version that reads
    // it.
    const existingId = saved?.flow.id ?? editing?.id;
    if (existingId !== undefined && !requiresIdentity) {
      saveSettings(existingId, () => saveSpec(finish));
      return;
    }
    saveSpec((flow) => saveSettings(flow.id, () => finish(flow)));
  }

  return (
    <Card>
      <form onSubmit={submit} noValidate>
        <div className="flex flex-col gap-5 p-6">
          <div>
            <h2 className="font-display text-[16px] font-bold">
              {editing
                ? t("identityProofingFlows.new.editTitle", {
                    name: editing.name,
                  })
                : t("identityProofingFlows.new.title")}
            </h2>
            {note && <p className={`${HINT} mt-1 font-semibold`}>{note}</p>}
          </div>

          <div className="flex max-w-md flex-col gap-1.5">
            <label htmlFor={NAME_ID} className="text-[14px] font-semibold">
              {t("identityProofingFlows.new.name")}
            </label>
            <Input
              id={NAME_ID}
              placeholder={t("identityProofingFlows.new.namePlaceholder")}
              value={draft.name}
              aria-invalid={shown === "name" || undefined}
              aria-describedby={
                shown === "name"
                  ? `${NAME_HINT_ID} ${NAME_ERROR_ID}`
                  : NAME_HINT_ID
              }
              onChange={(event) => update({ name: event.target.value })}
            />
            <span id={NAME_HINT_ID} className={HINT}>
              {t("identityProofingFlows.editor.nameHint")}
            </span>
            {shown === "name" && (
              <p id={NAME_ERROR_ID} role="alert" className={ERROR}>
                {t("identityProofingFlows.new.errors.name")}
              </p>
            )}
          </div>

          <fieldset className="flex flex-col gap-2.5">
            <legend className="mb-1 text-[14px] font-semibold">
              {t("identityProofingFlows.editor.level")}
            </legend>
            <p className={HINT}>
              {t("identityProofingFlows.editor.levelHint")}
            </p>
            {LEVEL_CHOICES.map((choice) => (
              <LevelOption
                key={choice}
                choice={choice}
                selected={draft.assuranceLevel === levelOf(choice)}
                onSelect={() =>
                  setDraft((current) =>
                    withAssuranceLevel(current, levelOf(choice)),
                  )
                }
              />
            ))}
            {shown === "assuranceLevel" && (
              <p role="alert" className={ERROR}>
                {t("identityProofingFlows.new.errors.assuranceLevel")}
              </p>
            )}
          </fieldset>

          <div className="rounded-yivi bg-surface-2 text-ink-soft flex items-center gap-2.5 px-3.5 py-3 text-[13.5px]">
            <Icon name="info" size={INFO_SIZE} />
            {t("identityProofingFlows.editor.regula")}
          </div>
        </div>

        <div className="border-line border-t">
          <button
            type="button"
            aria-expanded={advancedOpen}
            aria-controls={ADVANCED_ID}
            className="flex min-h-14 w-full cursor-pointer items-center justify-between gap-3 px-6 text-left"
            onClick={() => setAdvancedOpen((open) => !open)}
          >
            <span className="flex flex-col gap-0.5">
              <span className="font-display text-[16px] font-semibold">
                {t("identityProofingFlows.editor.advanced.title")}
              </span>
              <span className="text-ink-soft text-[13px]">
                {t("identityProofingFlows.editor.advanced.hint")}
              </span>
            </span>
            <Icon
              name={advancedOpen ? "chevron_up" : "chevron_down"}
              size={CHEVRON_SIZE}
            />
          </button>
          {advancedOpen && (
            <div id={ADVANCED_ID}>
              <AdvancedSettings
                draft={draft}
                update={update}
                settings={settings.data}
                diplomaMode={diplomaMode}
                onDiploma={chooseDiplomas}
                diplomaError={
                  saveDiplomas.isError
                    ? proofingErrorMessage(saveDiplomas.error, t)
                    : null
                }
                addressMode={addressMode}
                onAddress={setAddressMode}
                addressError={
                  saveAddress.isError
                    ? proofingErrorMessage(saveAddress.error, t)
                    : null
                }
                requiresIdentity={requiresIdentity}
                error={shown}
              />
            </div>
          )}
        </div>

        {(save.isError || shown === "faceMatchThreshold") && (
          <p role="alert" className={`${ERROR} border-line border-t px-6 py-3`}>
            {save.isError
              ? proofingErrorMessage(save.error, t)
              : t("identityProofingFlows.new.errors.faceMatchThreshold")}
          </p>
        )}
        <div className="border-line bg-surface-2 rounded-b-yivi flex flex-wrap items-center justify-between gap-3 border-t px-6 py-4">
          {editing && (
            <span className="text-ink-soft text-[13px]">
              {t("identityProofingFlows.editor.footer", { version })}
            </span>
          )}
          <div className="ml-auto flex gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" loading={save.isPending}>
              {editing
                ? t("identityProofingFlows.new.saveVersion", { version })
                : t("identityProofingFlows.new.create")}
            </Button>
          </div>
        </div>
      </form>
    </Card>
  );
}

// One level as a card with a radio: what it is and what the person does.
function LevelOption({
  choice,
  selected,
  onSelect,
}: {
  choice: LevelChoice;
  selected: boolean;
  onSelect: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <label
      className={`rounded-yivi has-[:focus-visible]:ring-ink/10 flex cursor-pointer items-start gap-3 border px-4 py-3.5 has-[:focus-visible]:ring-3 ${
        selected ? "border-ink bg-surface-2" : "border-line-strong bg-surface"
      }`}
    >
      <input
        type="radio"
        name="flow-level"
        className="accent-ink mt-0.5 h-[18px] w-[18px] flex-none"
        checked={selected}
        onChange={onSelect}
      />
      <span className="flex flex-col gap-1">
        <span className="text-[14px] font-semibold">
          {t(`identityProofingFlows.editor.levels.${choice}.title`)}
        </span>
        <span className="text-ink-soft text-[13.5px] leading-[21px]">
          {t(`identityProofingFlows.editor.levels.${choice}.hint`)}
        </span>
        {choice === "low" && (
          <span className={`${HINT} leading-[19px]`}>
            {t("identityProofingFlows.editor.levels.low.caveat")}
          </span>
        )}
      </span>
    </label>
  );
}
