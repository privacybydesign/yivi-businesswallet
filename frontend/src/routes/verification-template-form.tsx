import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { AttestationSchema } from "../api/attestations";
import { useCreateVerificationTemplateMutation } from "../api/verifications.queries";
import { control } from "../lib/attestation-form";
import { parseClaimList } from "../lib/verification";
import { Button, Modal } from "../ui";
import { Field } from "./attestations-fields";

const FORM_ID = "verification-template-form";
const SCHEMA_LIST_ID = "verification-template-vcts";

interface Props {
  slug: string;
  // The organization's own schemas, offered as vct suggestions; a template may
  // also name a credential type another organization issues.
  schemas: AttestationSchema[];
  onClose: () => void;
}

export function VerificationTemplateForm({
  slug,
  schemas,
  onClose,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateVerificationTemplateMutation(slug);

  const [name, setName] = useState("");
  const [vct, setVct] = useState("");
  const [claimsText, setClaimsText] = useState("");
  const [purpose, setPurpose] = useState("");
  const [attempted, setAttempted] = useState(false);

  const trimmedName = name.trim();
  const trimmedVct = vct.trim();
  const claims = useMemo(() => parseClaimList(claimsText), [claimsText]);
  const nameError = attempted && trimmedName === "";
  const vctError = attempted && trimmedVct === "";
  const claimsError = attempted && claims.length === 0;

  // When the vct names one of the org's own schemas, its attribute keys are the
  // claims a holder can disclose; offer them as a one-click fill.
  const schemaClaims = useMemo(
    () =>
      schemas
        .find((schema) => schema.vct === trimmedVct)
        ?.attributes.map((attribute) => attribute.key) ?? [],
    [schemas, trimmedVct],
  );

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (create.isPending) {
      return;
    }
    if (trimmedName === "" || trimmedVct === "" || claims.length === 0) {
      return;
    }
    create.mutate(
      {
        name: trimmedName,
        vct: trimmedVct,
        claims,
        purpose: purpose.trim(),
      },
      { onSuccess: onClose },
    );
  }

  return (
    <Modal
      title={t("verifications.templateForm.title")}
      closeLabel={t("common.cancel")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" form={FORM_ID} loading={create.isPending}>
            {t("verifications.templateForm.create")}
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
          id="verification-template-name"
          label={t("verifications.templateForm.name")}
          required
          error={
            nameError ? t("verifications.templateForm.nameRequired") : undefined
          }
        >
          <input
            id="verification-template-name"
            className={`${control(nameError)} h-9`}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>

        <Field
          id="verification-template-vct"
          label={t("verifications.templateForm.vct")}
          required
          error={
            vctError ? t("verifications.templateForm.vctRequired") : undefined
          }
        >
          <input
            id="verification-template-vct"
            className={`${control(vctError)} h-9 font-mono text-[12.5px]`}
            value={vct}
            list={SCHEMA_LIST_ID}
            onChange={(event) => setVct(event.target.value)}
          />
        </Field>
        <datalist id={SCHEMA_LIST_ID}>
          {schemas.map((schema) => (
            <option key={schema.id} value={schema.vct}>
              {schema.displayName}
            </option>
          ))}
        </datalist>

        <Field
          id="verification-template-claims"
          label={t("verifications.templateForm.claims")}
          required
          error={
            claimsError
              ? t("verifications.templateForm.claimsRequired")
              : undefined
          }
        >
          <textarea
            id="verification-template-claims"
            className={`${control(claimsError)} min-h-20 py-2 font-mono text-[12.5px]`}
            value={claimsText}
            onChange={(event) => setClaimsText(event.target.value)}
          />
        </Field>
        {schemaClaims.length > 0 ? (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-ink-soft text-[12px]">
              {t("verifications.templateForm.schemaClaims", {
                claims: schemaClaims.join(", "),
              })}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setClaimsText(schemaClaims.join(", "))}
            >
              {t("verifications.templateForm.useSchemaClaims")}
            </Button>
          </div>
        ) : (
          <p className="text-ink-soft text-[12px]">
            {t("verifications.templateForm.claimsHint")}
          </p>
        )}

        <Field
          id="verification-template-purpose"
          label={t("verifications.templateForm.purpose")}
        >
          <input
            id="verification-template-purpose"
            className={`${control(false)} h-9`}
            value={purpose}
            onChange={(event) => setPurpose(event.target.value)}
          />
        </Field>

        {create.isError && (
          <p role="alert" className="text-error text-[12.5px]">
            {t("common.saveError", { message: create.error.message })}
          </p>
        )}
      </form>
    </Modal>
  );
}
