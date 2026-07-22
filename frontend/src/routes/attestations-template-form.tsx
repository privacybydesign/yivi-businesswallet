import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type {
  AttestationAttribute,
  AttestationKey,
  AttestationSchema,
  AttestationSubjectType,
  AttestationTemplate,
} from "../api/attestations";
import { SUBJECT_SOURCE_FIELDS } from "../api/attestations";
import {
  useCreateAttestationTemplateMutation,
  useUpdateAttestationTemplateMutation,
} from "../api/attestations.queries";
import { Button, Modal } from "../ui";
import { control } from "../lib/attestation-form";
import { Field } from "./attestations-fields";
import type { TFunction } from "i18next";

const DEFAULT_STATUS = "active";
const DECIMAL_RADIX = 10;

// sourceLabel maps a subject-source token to its i18n label. A literal-key switch
// (not a dynamic key) keeps the typed-i18n guarantee, mirroring lib/audit-event.ts.
function sourceLabel(t: TFunction, token: string): string {
  switch (token) {
    case "member.givenNames":
      return t("attestations.sources.givenNames");
    case "member.lastName":
      return t("attestations.sources.lastName");
    case "member.fullName":
      return t("attestations.sources.fullName");
    case "member.preferredName":
      return t("attestations.sources.preferredName");
    case "member.email":
      return t("attestations.sources.email");
    case "member.phone":
      return t("attestations.sources.phone");
    case "member.role":
      return t("attestations.sources.role");
    case "member.jobTitle":
      return t("attestations.sources.jobTitle");
    case "member.department":
      return t("attestations.sources.department");
    case "org.name":
      return t("attestations.sources.orgName");
    case "org.kvkNumber":
      return t("attestations.sources.orgKvkNumber");
    case "org.euid":
      return t("attestations.sources.orgEuid");
    case "org.digitalAddress":
      return t("attestations.sources.orgDigitalAddress");
    default:
      return token;
  }
}

function buildDefaults(
  attributes: AttestationAttribute[],
  values: Record<string, string>,
  sources: Record<string, string>,
): Record<string, string> | undefined {
  const result: Record<string, string> = {};
  for (const attribute of attributes) {
    // A source-bound attribute is pre-filled from the subject, not a static value.
    if ((sources[attribute.key] ?? "") !== "") {
      continue;
    }
    const value = values[attribute.key]?.trim() ?? "";
    if (value !== "") {
      result[attribute.key] = value;
    }
  }
  return Object.keys(result).length > 0 ? result : undefined;
}

// buildSources keeps only bindings for attributes still declared by the schema,
// dropping any stale entry left over from a different schema/subject type.
function buildSources(
  attributes: AttestationAttribute[],
  values: Record<string, string>,
): Record<string, string> | undefined {
  const result: Record<string, string> = {};
  for (const attribute of attributes) {
    const token = values[attribute.key] ?? "";
    if (token !== "") {
      result[attribute.key] = token;
    }
  }
  return Object.keys(result).length > 0 ? result : undefined;
}

interface Props {
  slug: string;
  // The template to edit, or undefined to create a new one.
  template?: AttestationTemplate;
  schemas: AttestationSchema[];
  keys: AttestationKey[];
  onClose: () => void;
}

export function AttestationTemplateForm({
  slug,
  template,
  schemas,
  keys,
  onClose,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const isEdit = template !== undefined;
  const create = useCreateAttestationTemplateMutation(slug);
  const update = useUpdateAttestationTemplateMutation(slug);

  const [schemaId, setSchemaId] = useState(template?.schemaId ?? "");
  const [name, setName] = useState(template?.name ?? "");
  const [validity, setValidity] = useState(
    template?.validitySeconds !== undefined
      ? String(template.validitySeconds)
      : "",
  );
  const [keyMaterialId, setKeyMaterialId] = useState(
    template?.keyMaterialId ?? "",
  );
  const [status, setStatus] = useState(template?.status ?? DEFAULT_STATUS);
  const [defaults, setDefaults] = useState<Record<string, string>>(
    template?.defaultAttributes ?? {},
  );
  const [sources, setSources] = useState<Record<string, string>>(
    template?.attributeSources ?? {},
  );
  const [attempted, setAttempted] = useState(false);

  const pending = create.isPending || update.isPending;
  const mutationError = create.error ?? update.error;
  const showError = create.isError || update.isError;

  // The attribute list to prefill: from the chosen schema when creating, or
  // baked into the template when editing.
  const attributes = useMemo<AttestationAttribute[]>(() => {
    if (isEdit && template) {
      return template.attributes;
    }
    return schemas.find((s) => s.id === schemaId)?.attributes ?? [];
  }, [isEdit, template, schemas, schemaId]);

  // The subject type drives which subject fields an attribute can be copied from.
  const subjectType = useMemo<AttestationSubjectType | undefined>(() => {
    if (isEdit && template) {
      return template.subjectType;
    }
    return schemas.find((s) => s.id === schemaId)?.subjectType;
  }, [isEdit, template, schemas, schemaId]);
  const sourceOptions = subjectType ? SUBJECT_SOURCE_FIELDS[subjectType] : [];

  const trimmedName = name.trim();
  const nameError = attempted && trimmedName === "";
  const schemaError = attempted && !isEdit && schemaId === "";

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (pending) {
      return;
    }
    if (trimmedName === "" || (!isEdit && schemaId === "")) {
      return;
    }
    const parsedValidity =
      validity.trim() === ""
        ? undefined
        : Number.parseInt(validity, DECIMAL_RADIX);
    const attributeSources = buildSources(attributes, sources);
    const defaultAttributes = buildDefaults(attributes, defaults, sources);
    const keyId = keyMaterialId === "" ? undefined : keyMaterialId;

    if (isEdit && template) {
      update.mutate(
        {
          templateId: template.id,
          input: {
            name: trimmedName,
            defaultAttributes,
            attributeSources,
            validitySeconds: parsedValidity,
            keyMaterialId: keyId,
            status: status.trim() || DEFAULT_STATUS,
          },
        },
        { onSuccess: onClose },
      );
    } else {
      create.mutate(
        {
          schemaId,
          name: trimmedName,
          defaultAttributes,
          attributeSources,
          validitySeconds: parsedValidity,
          keyMaterialId: keyId,
        },
        { onSuccess: onClose },
      );
    }
  }

  const FORM_ID = "attestation-template-form";

  return (
    <Modal
      title={
        isEdit
          ? t("attestations.templateForm.editTitle")
          : t("attestations.templateForm.createTitle")
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
              ? t("attestations.templateForm.save")
              : t("attestations.templateForm.create")}
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
        <Field
          id="template-name"
          label={t("attestations.templateForm.name")}
          required
          error={
            nameError ? t("attestations.templateForm.nameRequired") : undefined
          }
        >
          <input
            id="template-name"
            className={`${control(nameError)} h-9`}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>

        {isEdit ? (
          <Field
            id="template-schema"
            label={t("attestations.templateForm.schema")}
          >
            <input
              id="template-schema"
              className={`${control(false)} h-9`}
              value={template ? template.displayName : ""}
              readOnly
              disabled
            />
          </Field>
        ) : (
          <Field
            id="template-schema"
            label={t("attestations.templateForm.schema")}
            required
            error={
              schemaError
                ? t("attestations.templateForm.schemaRequired")
                : undefined
            }
          >
            <select
              id="template-schema"
              className={`${control(schemaError)} h-9`}
              value={schemaId}
              onChange={(event) => setSchemaId(event.target.value)}
            >
              <option value="">
                {t("attestations.templateForm.selectSchema")}
              </option>
              {schemas.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.displayName}
                </option>
              ))}
            </select>
          </Field>
        )}

        <div className="grid grid-cols-2 gap-4">
          <Field
            id="template-validity"
            label={t("attestations.templateForm.validitySeconds")}
          >
            <input
              id="template-validity"
              type="number"
              min={0}
              className={`${control(false)} h-9`}
              value={validity}
              onChange={(event) => setValidity(event.target.value)}
            />
          </Field>
          <Field id="template-key" label={t("attestations.templateForm.key")}>
            <select
              id="template-key"
              className={`${control(false)} h-9`}
              value={keyMaterialId}
              onChange={(event) => setKeyMaterialId(event.target.value)}
            >
              <option value="">{t("attestations.templateForm.noKey")}</option>
              {keys.map((key) => (
                <option key={key.id} value={key.id}>
                  {key.label}
                </option>
              ))}
            </select>
          </Field>
        </div>

        {isEdit && (
          <Field
            id="template-status"
            label={t("attestations.templateForm.status")}
          >
            <input
              id="template-status"
              className={`${control(false)} h-9`}
              value={status}
              onChange={(event) => setStatus(event.target.value)}
            />
          </Field>
        )}

        {attributes.length > 0 && (
          <div className="flex flex-col gap-2">
            <span className="text-ink-soft text-[12px] font-semibold">
              {t("attestations.templateForm.defaultAttributes")}
            </span>
            <div className="flex flex-col gap-3">
              {attributes.map((attribute) => {
                const boundToken = sources[attribute.key] ?? "";
                const isBound = boundToken !== "";
                return (
                  <Field
                    key={attribute.key}
                    id={`template-default-${attribute.key}`}
                    label={attribute.label || attribute.key}
                  >
                    <div className="flex flex-col gap-1">
                      {sourceOptions.length > 0 && (
                        <select
                          aria-label={t("attestations.templateForm.copyFrom", {
                            attribute: attribute.label || attribute.key,
                          })}
                          className={`${control(false)} h-9`}
                          value={boundToken}
                          onChange={(event) =>
                            setSources((current) => ({
                              ...current,
                              [attribute.key]: event.target.value,
                            }))
                          }
                        >
                          <option value="">
                            {t("attestations.templateForm.noSource")}
                          </option>
                          {sourceOptions.map((token) => (
                            <option key={token} value={token}>
                              {sourceLabel(t, token)}
                            </option>
                          ))}
                        </select>
                      )}
                      <input
                        id={`template-default-${attribute.key}`}
                        className={`${control(false)} h-9`}
                        value={defaults[attribute.key] ?? ""}
                        disabled={isBound}
                        placeholder={
                          isBound
                            ? t("attestations.templateForm.sourcePrefill")
                            : undefined
                        }
                        onChange={(event) =>
                          setDefaults((current) => ({
                            ...current,
                            [attribute.key]: event.target.value,
                          }))
                        }
                      />
                    </div>
                  </Field>
                );
              })}
            </div>
          </div>
        )}

        {showError && mutationError && (
          <p
            role="alert"
            className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
          >
            {t("attestations.templateForm.error", {
              message: mutationError.message,
            })}
          </p>
        )}
      </form>
    </Modal>
  );
}
