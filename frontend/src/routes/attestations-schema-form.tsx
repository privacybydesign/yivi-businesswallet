import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationAttribute,
  AttestationSchema,
  AttestationSubjectType,
  LocalizedName,
} from "../api/attestations";
import { SUPPORTED_ATTRIBUTE_TYPES } from "../api/attestations";
import {
  useAttestationSchemaIssuerConfigQuery,
  useCreateAttestationSchemaMutation,
  useUpdateAttestationSchemaMutation,
} from "../api/attestations.queries";
import { Button, Modal } from "../ui";
import { control } from "../lib/attestation-form";
import { Field } from "./attestations-fields";
import { JsonSnippet } from "./json-snippet";

const DEFAULT_STATUS = "active";
const DEFAULT_ATTRIBUTE_TYPE = "string";
const SUBJECT_NATURAL_PERSON = "natural_person";
const SUBJECT_ORGANIZATION = "organization";
const SUBJECT_TYPES: readonly AttestationSubjectType[] = [
  SUBJECT_NATURAL_PERSON,
  SUBJECT_ORGANIZATION,
];

// A per-language display entry as edited in the form: a BCP-47 language tag and
// the shown text. Converted to the API's {lang,name} / {lang,label} shapes on
// submit (see toNames / toLabels).
interface Translation {
  lang: string;
  text: string;
}

interface Row {
  key: string;
  label: string;
  type: string;
  required: boolean;
  translations: Translation[];
}

function emptyRow(): Row {
  return {
    key: "",
    label: "",
    type: DEFAULT_ATTRIBUTE_TYPE,
    required: false,
    translations: [],
  };
}

function schemaRow(attr: AttestationAttribute): Row {
  return {
    key: attr.key,
    label: attr.label,
    type: attr.type,
    required: attr.required,
    translations: (attr.display ?? []).map((d) => ({
      lang: d.lang,
      text: d.label,
    })),
  };
}

// Drops fully-empty entries and trims the rest. Partial rows (a language without
// text, or the reverse) are kept so the backend can reject them with a message.
function cleanTranslations(entries: Translation[]): Translation[] {
  return entries
    .map((e) => ({ lang: e.lang.trim(), text: e.text.trim() }))
    .filter((e) => e.lang !== "" || e.text !== "");
}

interface Props {
  slug: string;
  // The schema to edit, or undefined to create a new one.
  schema?: AttestationSchema;
  onClose: () => void;
}

export function AttestationSchemaForm({
  slug,
  schema,
  onClose,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const isEdit = schema !== undefined;
  const create = useCreateAttestationSchemaMutation(slug);
  const update = useUpdateAttestationSchemaMutation(slug);

  const [vct, setVct] = useState(schema?.vct ?? "");
  const [displayName, setDisplayName] = useState(schema?.displayName ?? "");
  const [credentialConfigId, setCredentialConfigId] = useState(
    schema?.credentialConfigId ?? "",
  );
  const [subjectType, setSubjectType] = useState<AttestationSubjectType>(
    schema?.subjectType ?? SUBJECT_NATURAL_PERSON,
  );
  const [qualified, setQualified] = useState(schema?.qualified ?? false);
  const [status, setStatus] = useState(schema?.status ?? DEFAULT_STATUS);
  const [displayNames, setDisplayNames] = useState<Translation[]>(
    (schema?.display ?? []).map((d) => ({ lang: d.lang, text: d.name })),
  );
  const [rows, setRows] = useState<Row[]>(
    schema && schema.attributes.length > 0
      ? schema.attributes.map(schemaRow)
      : [emptyRow()],
  );
  const [attempted, setAttempted] = useState(false);
  const [showIssuerConfig, setShowIssuerConfig] = useState(false);

  const issuerConfig = useAttestationSchemaIssuerConfigQuery(
    slug,
    schema?.id ?? "",
    isEdit && showIssuerConfig,
  );

  const pending = create.isPending || update.isPending;
  const mutationError = create.error ?? update.error;
  const showError = create.isError || update.isError;

  const trimmedVct = vct.trim();
  const trimmedName = displayName.trim();
  const trimmedConfig = credentialConfigId.trim();
  const attributes = rows
    .map((row) => ({
      key: row.key.trim(),
      label: row.label.trim(),
      type: row.type.trim() || DEFAULT_ATTRIBUTE_TYPE,
      required: row.required,
      display: cleanTranslations(row.translations).map((e) => ({
        lang: e.lang,
        label: e.text,
      })),
    }))
    .filter((row) => row.key !== "");
  const display: LocalizedName[] = cleanTranslations(displayNames).map((e) => ({
    lang: e.lang,
    name: e.text,
  }));

  const vctError = attempted && !isEdit && trimmedVct === "";
  const nameError = attempted && trimmedName === "";
  const configError = attempted && trimmedConfig === "";
  const attributesError = attempted && attributes.length === 0;

  function setRow(index: number, patch: Partial<Row>): void {
    setRows((current) =>
      current.map((row, i) => (i === index ? { ...row, ...patch } : row)),
    );
  }

  function addRow(): void {
    setRows((current) => [...current, emptyRow()]);
  }

  function removeRow(index: number): void {
    setRows((current) => current.filter((_, i) => i !== index));
  }

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (pending) {
      return;
    }
    if (
      (!isEdit && trimmedVct === "") ||
      trimmedName === "" ||
      trimmedConfig === "" ||
      attributes.length === 0
    ) {
      return;
    }
    if (isEdit && schema) {
      update.mutate(
        {
          schemaId: schema.id,
          input: {
            displayName: trimmedName,
            credentialConfigId: trimmedConfig,
            attributes,
            display,
            subjectType,
            qualified,
            status: status.trim() || DEFAULT_STATUS,
          },
        },
        { onSuccess: onClose },
      );
    } else {
      create.mutate(
        {
          vct: trimmedVct,
          displayName: trimmedName,
          credentialConfigId: trimmedConfig,
          attributes,
          display,
          subjectType,
          qualified,
          status: status.trim() || DEFAULT_STATUS,
        },
        { onSuccess: onClose },
      );
    }
  }

  const FORM_ID = "attestation-schema-form";

  return (
    <Modal
      title={
        isEdit
          ? t("attestations.schemaForm.editTitle")
          : t("attestations.schemaForm.createTitle")
      }
      closeLabel={t("common.cancel")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form={FORM_ID} loading={pending}>
            {isEdit
              ? t("attestations.schemaForm.save")
              : t("attestations.schemaForm.create")}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        onSubmit={handleSubmit}
        noValidate
        className="flex flex-col gap-4"
      >
        {!isEdit && (
          <Field
            id="schema-vct"
            label={t("attestations.schemaForm.vct")}
            required
            error={
              vctError ? t("attestations.schemaForm.vctRequired") : undefined
            }
          >
            <input
              id="schema-vct"
              className={`${control(vctError)} h-9`}
              value={vct}
              onChange={(event) => setVct(event.target.value)}
              placeholder={t("attestations.schemaForm.vctPlaceholder")}
            />
          </Field>
        )}

        <Field
          id="schema-name"
          label={t("attestations.schemaForm.displayName")}
          required
          error={
            nameError
              ? t("attestations.schemaForm.displayNameRequired")
              : undefined
          }
        >
          <input
            id="schema-name"
            className={`${control(nameError)} h-9`}
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </Field>

        <Field
          id="schema-config"
          label={t("attestations.schemaForm.credentialConfigId")}
          required
          error={
            configError
              ? t("attestations.schemaForm.credentialConfigRequired")
              : undefined
          }
        >
          <input
            id="schema-config"
            className={`${control(configError)} h-9`}
            value={credentialConfigId}
            onChange={(event) => setCredentialConfigId(event.target.value)}
          />
        </Field>

        <div className="flex flex-col gap-2">
          <span className="text-ink-soft text-[12px] font-semibold">
            {t("attestations.schemaForm.displayNames")}
          </span>
          <TranslationList
            entries={displayNames}
            onChange={setDisplayNames}
            textLabel={t("attestations.schemaForm.displayName")}
            textPlaceholder={t("attestations.schemaForm.displayName")}
          />
        </div>

        <Field
          id="schema-subject-type"
          label={t("attestations.schemaForm.subjectType")}
        >
          <select
            id="schema-subject-type"
            className={`${control(false)} h-9`}
            value={subjectType}
            onChange={(event) =>
              setSubjectType(event.target.value as AttestationSubjectType)
            }
          >
            {SUBJECT_TYPES.map((value) => (
              <option key={value} value={value}>
                {value === SUBJECT_NATURAL_PERSON
                  ? t("attestations.schemaForm.subjectNaturalPerson")
                  : t("attestations.schemaForm.subjectOrganization")}
              </option>
            ))}
          </select>
        </Field>

        <div className="grid grid-cols-2 gap-4">
          <Field id="schema-status" label={t("attestations.schemaForm.status")}>
            <input
              id="schema-status"
              className={`${control(false)} h-9`}
              value={status}
              onChange={(event) => setStatus(event.target.value)}
            />
          </Field>
          <div className="flex items-end pb-1.5">
            <label className="text-ink flex cursor-pointer items-center gap-2 text-[13.5px]">
              <input
                type="checkbox"
                checked={qualified}
                onChange={(event) => setQualified(event.target.checked)}
              />
              {t("attestations.schemaForm.qualified")}
            </label>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between">
            <span className="text-ink-soft text-[12px] font-semibold">
              {t("attestations.schemaForm.attributes")}
            </span>
            <Button variant="ghost" size="sm" icon="add" onClick={addRow}>
              {t("attestations.schemaForm.addAttribute")}
            </Button>
          </div>
          {attributesError && (
            <span role="alert" className="text-error text-[12px]">
              {t("attestations.schemaForm.attributesRequired")}
            </span>
          )}
          <div className="flex flex-col gap-2">
            {rows.map((row, index) => (
              <div
                key={index}
                className="border-line bg-surface-2 flex flex-col gap-2 rounded-md border p-2.5"
              >
                <div className="grid grid-cols-3 gap-2">
                  <input
                    className={`${control(false)} h-8`}
                    value={row.key}
                    onChange={(event) =>
                      setRow(index, { key: event.target.value })
                    }
                    placeholder={t("attestations.schemaForm.attrKey")}
                    aria-label={t("attestations.schemaForm.attrKey")}
                  />
                  <input
                    className={`${control(false)} h-8`}
                    value={row.label}
                    onChange={(event) =>
                      setRow(index, { label: event.target.value })
                    }
                    placeholder={t("attestations.schemaForm.attrLabel")}
                    aria-label={t("attestations.schemaForm.attrLabel")}
                  />
                  <select
                    className={`${control(false)} h-8`}
                    value={row.type}
                    onChange={(event) =>
                      setRow(index, { type: event.target.value })
                    }
                    aria-label={t("attestations.schemaForm.attrType")}
                  >
                    {SUPPORTED_ATTRIBUTE_TYPES.map((value) => (
                      <option key={value} value={value}>
                        {t(`attestations.schemaForm.types.${value}`)}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex items-center justify-between">
                  <label className="text-ink-soft flex cursor-pointer items-center gap-2 text-[12.5px]">
                    <input
                      type="checkbox"
                      checked={row.required}
                      onChange={(event) =>
                        setRow(index, { required: event.target.checked })
                      }
                    />
                    {t("attestations.schemaForm.attrRequired")}
                  </label>
                  {rows.length > 1 && (
                    <Button
                      variant="dangerGhost"
                      size="sm"
                      icon="delete"
                      iconOnly
                      onClick={() => removeRow(index)}
                      aria-label={t("attestations.schemaForm.removeAttribute")}
                    />
                  )}
                </div>
                <div className="border-line flex flex-col gap-1.5 border-t pt-2">
                  <span className="text-ink-soft text-[11.5px] font-semibold">
                    {t("attestations.schemaForm.attrTranslations")}
                  </span>
                  <TranslationList
                    entries={row.translations}
                    onChange={(next) => setRow(index, { translations: next })}
                    textLabel={t("attestations.schemaForm.attrLabel")}
                    textPlaceholder={t("attestations.schemaForm.attrLabel")}
                  />
                </div>
              </div>
            ))}
          </div>
        </div>

        {isEdit && (
          <div className="border-line flex flex-col gap-2 border-t pt-3">
            <div className="flex items-center justify-between">
              <span className="text-ink-soft text-[12px] font-semibold">
                {t("attestations.schemaForm.issuerConfig")}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setShowIssuerConfig((v) => !v)}
              >
                {showIssuerConfig
                  ? t("attestations.schemaForm.issuerConfigHide")
                  : t("attestations.schemaForm.issuerConfigShow")}
              </Button>
            </div>
            {showIssuerConfig && (
              <>
                <p className="text-ink-soft text-[12px]">
                  {t("attestations.schemaForm.issuerConfigHint")}
                </p>
                {issuerConfig.isPending && (
                  <span className="text-ink-soft text-[12px]">
                    {t("common.loading")}
                  </span>
                )}
                {issuerConfig.isError && (
                  <span role="alert" className="text-error text-[12px]">
                    {t("attestations.schemaForm.issuerConfigError")}
                  </span>
                )}
                {issuerConfig.data && (
                  <div className="flex flex-col gap-3">
                    <JsonSnippet
                      title={t("attestations.schemaForm.issuerConfigMetadata")}
                      value={issuerConfig.data.metadata}
                    />
                    <JsonSnippet
                      title={t("attestations.schemaForm.issuerConfigVct")}
                      value={issuerConfig.data.vct}
                    />
                  </div>
                )}
              </>
            )}
          </div>
        )}

        {showError && mutationError && (
          <p
            role="alert"
            className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
          >
            {t("attestations.schemaForm.error", {
              message: mutationError.message,
            })}
          </p>
        )}
      </form>
    </Modal>
  );
}

// TranslationList edits a list of per-language display entries (a language tag +
// the shown text). Used for both the credential display names and a single
// attribute's labels; the parent owns the entries and converts them to the API
// shape on submit.
function TranslationList({
  entries,
  onChange,
  textLabel,
  textPlaceholder,
}: {
  entries: Translation[];
  onChange: (next: Translation[]) => void;
  textLabel: string;
  textPlaceholder: string;
}): React.JSX.Element {
  const { t } = useTranslation();

  function update(index: number, patch: Partial<Translation>): void {
    onChange(entries.map((e, i) => (i === index ? { ...e, ...patch } : e)));
  }

  return (
    <div className="flex flex-col gap-2">
      {entries.map((entry, index) => (
        <div key={index} className="grid grid-cols-[5rem_1fr_auto] gap-2">
          <input
            className={`${control(false)} h-8`}
            value={entry.lang}
            onChange={(event) => update(index, { lang: event.target.value })}
            placeholder={t(
              "attestations.schemaForm.translationLangPlaceholder",
            )}
            aria-label={t("attestations.schemaForm.translationLang")}
          />
          <input
            className={`${control(false)} h-8`}
            value={entry.text}
            onChange={(event) => update(index, { text: event.target.value })}
            placeholder={textPlaceholder}
            aria-label={textLabel}
          />
          <Button
            variant="dangerGhost"
            size="sm"
            icon="delete"
            iconOnly
            onClick={() => onChange(entries.filter((_, i) => i !== index))}
            aria-label={t("attestations.schemaForm.removeTranslation")}
          />
        </div>
      ))}
      <div>
        <Button
          variant="ghost"
          size="sm"
          icon="add"
          onClick={() => onChange([...entries, { lang: "", text: "" }])}
        >
          {t("attestations.schemaForm.addTranslation")}
        </Button>
      </div>
    </div>
  );
}
