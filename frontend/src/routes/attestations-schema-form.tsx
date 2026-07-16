import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationAttribute,
  AttestationSchema,
  AttestationSubjectType,
} from "../api/attestations";
import {
  useCreateAttestationSchemaMutation,
  useUpdateAttestationSchemaMutation,
} from "../api/attestations.queries";
import { Button, Modal } from "../ui";
import { control } from "../lib/attestation-form";
import { Field } from "./attestations-fields";

const DEFAULT_STATUS = "active";
const DEFAULT_ATTRIBUTE_TYPE = "string";
const SUBJECT_NATURAL_PERSON = "natural_person";
const SUBJECT_ORGANIZATION = "organization";
const SUBJECT_TYPES: readonly AttestationSubjectType[] = [
  SUBJECT_NATURAL_PERSON,
  SUBJECT_ORGANIZATION,
];

type Row = AttestationAttribute;

function emptyRow(): Row {
  return { key: "", label: "", type: DEFAULT_ATTRIBUTE_TYPE, required: false };
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
  const [rows, setRows] = useState<Row[]>(
    schema && schema.attributes.length > 0 ? schema.attributes : [emptyRow()],
  );
  const [attempted, setAttempted] = useState(false);

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
    }))
    .filter((row) => row.key !== "");

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
                  <input
                    className={`${control(false)} h-8`}
                    value={row.type}
                    onChange={(event) =>
                      setRow(index, { type: event.target.value })
                    }
                    placeholder={t("attestations.schemaForm.attrType")}
                    aria-label={t("attestations.schemaForm.attrType")}
                  />
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
              </div>
            ))}
          </div>
        </div>

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
