import { cloneElement, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import type { Mandate } from "../api/mandates";
import {
  useGrantMandateMutation,
  useMandateAuthorityQuery,
  useMandatesQuery,
  useRevokeMandateMutation,
} from "../api/mandates.queries";
import {
  useOrganizationDepartmentsQuery,
  useOrganizationMembersQuery,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { useDateFormatter } from "../lib/format-when";
import { fullName } from "../lib/name";
import {
  MANDATE_TYPES,
  MAX_REVOCATION_REASON_LENGTH,
  isoFromLocalInput,
  mandateCascade,
  mandateGrantAvailability,
  mandateIsRevocable,
  mandateLineage,
  mandateScopeLabel,
  mandateStatusLabel,
  mandateStatusTone,
  mandateTypeLabel,
} from "../lib/mandate";
import { Button, Card, Icon, Modal, Table, Tag } from "../ui";
import * as React from "react";

const COLUMN_COUNT = 7;
// One step of indentation per link in a delegation chain.
const LINEAGE_INDENT_PX = 18;
// One page of members for the grantee picker rather than paging inside a dialog,
// matching attestations-issue.tsx and signing.tsx.
const MEMBER_PAGE_SIZE = 200;

const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

function control(hasError: boolean): string {
  return [CONTROL, hasError ? CONTROL_ERR : CONTROL_OK].join(" ");
}

function errorCode(error: Error): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

// The backend's own prose is not localizable from here, so the sentinels the
// mandate handlers can answer with are mapped to copy of our own and anything
// else falls back to the raw message.
function mandateError(error: Error, t: TFunction): string {
  switch (errorCode(error)) {
    case "grantee_not_member":
      return t("mandates.errors.granteeNotMember");
    case "department_not_found":
      return t("mandates.errors.departmentNotFound");
    case "over_delegation":
      return t("mandates.errors.overDelegation");
    case "mandate_authority_required":
      return t("mandates.errors.authorityRequired");
    case "mandate_inactive":
      return t("mandates.errors.inactive");
    case "mandate_not_found":
      return t("mandates.errors.notFound");
    default:
      return t("mandates.errors.other", { message: error.message });
  }
}

// The register is gated on RequireOrgAdmin, which an admin whose own mandate
// lapsed no longer passes. Saying so beats "you are not a member": they are one,
// and their authority is what ran out.
function registerLoadError(error: Error, t: TFunction): string {
  if (errorCode(error) === "mandate_withdrawn") {
    return t("mandates.errors.withdrawn");
  }
  return t("mandates.loadError", { message: error.message });
}

// Field labels one control and links its hint and error to it, the way
// attestations-fields.tsx does: role="alert" only fires on first render, so the
// association is what makes the message reachable again on a later focus.
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
  error?: string;
  children: React.ReactElement<{
    "aria-describedby"?: string;
    "aria-invalid"?: boolean;
  }>;
}): React.JSX.Element {
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy = [hint ? hintId : "", error ? errorId : ""]
    .filter((part) => part !== "")
    .join(" ");
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className={FIELD_LABEL}>
        {label}
      </label>
      {cloneElement(children, {
        "aria-describedby": describedBy === "" ? undefined : describedBy,
        "aria-invalid": error ? true : undefined,
      })}
      {hint && (
        <span id={hintId} className="text-muted text-[12px]">
          {hint}
        </span>
      )}
      {error && (
        <span id={errorId} role="alert" className="text-error text-[12px]">
          {error}
        </span>
      )}
    </div>
  );
}

// GrantMandateDialog collects one grant. The backend clamps a delegated grant to
// the mandate it is cut from and re-derives the grantor's authority, so this form
// validates only what it can answer without a round trip.
function GrantMandateDialog({
  slug,
  onClose,
}: {
  slug: string;
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const members = useOrganizationMembersQuery(
    slug,
    { status: "active", limit: MEMBER_PAGE_SIZE },
    true,
  );
  const departments = useOrganizationDepartmentsQuery(slug);
  const grant = useGrantMandateMutation(slug);

  const [granteeUserId, setGranteeUserId] = useState("");
  const [type, setType] = useState<string>(MANDATE_TYPES[0]);
  const [scope, setScope] = useState("organization");
  const [departmentId, setDepartmentId] = useState("");
  const [validFrom, setValidFrom] = useState("");
  const [validUntil, setValidUntil] = useState("");
  const [attempted, setAttempted] = useState(false);

  const from = isoFromLocalInput(validFrom);
  const until = isoFromLocalInput(validUntil);
  const emptyWindow =
    from !== undefined && until !== undefined && until <= from;

  const granteeError = attempted && granteeUserId === "";
  const departmentError =
    attempted && scope === "department" && departmentId === "";

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (
      grant.isPending ||
      granteeUserId === "" ||
      emptyWindow ||
      (scope === "department" && departmentId === "")
    ) {
      return;
    }
    grant.mutate(
      {
        type,
        granteeUserId,
        scope,
        departmentId: scope === "department" ? departmentId : undefined,
        validFrom: from,
        validUntil: until,
      },
      { onSuccess: onClose },
    );
  }

  const formId = "mandate-grant-form";
  const candidates = (members.data?.entries ?? []).filter(
    (entry) => entry.userId !== null,
  );

  return (
    <Modal
      title={t("mandates.grantTitle")}
      closeLabel={t("common.cancel")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            type="submit"
            form={formId}
            size="sm"
            loading={grant.isPending}
          >
            {t("mandates.grant")}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        onSubmit={handleSubmit}
        noValidate
        className="flex flex-col gap-4"
      >
        <Field
          id="mandate-grantee"
          label={t("mandates.form.grantee")}
          hint={t("mandates.form.granteeHint")}
          error={granteeError ? t("mandates.form.granteeRequired") : undefined}
        >
          <select
            id="mandate-grantee"
            className={control(granteeError)}
            value={granteeUserId}
            onChange={(event) => setGranteeUserId(event.target.value)}
          >
            <option value="">{t("mandates.form.selectGrantee")}</option>
            {candidates.map((entry) => (
              <option key={entry.userId} value={entry.userId ?? ""}>
                {`${fullName(entry)} (${entry.email})`}
              </option>
            ))}
          </select>
        </Field>

        <Field
          id="mandate-type"
          label={t("mandates.form.type")}
          hint={
            type === "full"
              ? t("mandates.typeHints.full")
              : t("mandates.typeHints.administrative")
          }
        >
          <select
            id="mandate-type"
            className={control(false)}
            value={type}
            onChange={(event) => setType(event.target.value)}
          >
            {MANDATE_TYPES.map((option) => (
              <option key={option} value={option}>
                {mandateTypeLabel(option, t)}
              </option>
            ))}
          </select>
        </Field>

        <Field
          id="mandate-scope"
          label={t("mandates.form.scope")}
          hint={t("mandates.form.scopeHint")}
        >
          <select
            id="mandate-scope"
            className={control(false)}
            value={scope}
            onChange={(event) => setScope(event.target.value)}
          >
            <option value="organization">
              {t("mandates.scopes.organization")}
            </option>
            <option value="department">
              {t("mandates.scopes.department")}
            </option>
          </select>
        </Field>

        {scope === "department" && (
          <Field
            id="mandate-department"
            label={t("common.department")}
            error={
              departmentError
                ? t("mandates.form.departmentRequired")
                : undefined
            }
          >
            <select
              id="mandate-department"
              className={control(departmentError)}
              value={departmentId}
              onChange={(event) => setDepartmentId(event.target.value)}
            >
              <option value="">{t("mandates.form.selectDepartment")}</option>
              {departments.data?.map((department) => (
                <option key={department.id} value={department.id}>
                  {department.name}
                </option>
              ))}
            </select>
          </Field>
        )}

        <div className="grid grid-cols-2 gap-4">
          <Field
            id="mandate-valid-from"
            label={t("mandates.form.validFrom")}
            hint={t("mandates.form.validFromHint")}
          >
            <input
              id="mandate-valid-from"
              type="datetime-local"
              className={control(false)}
              value={validFrom}
              onChange={(event) => setValidFrom(event.target.value)}
            />
          </Field>
          <Field
            id="mandate-valid-until"
            label={t("mandates.form.validUntil")}
            hint={t("mandates.form.validUntilHint")}
            error={emptyWindow ? t("mandates.form.emptyWindow") : undefined}
          >
            <input
              id="mandate-valid-until"
              type="datetime-local"
              className={control(emptyWindow)}
              value={validUntil}
              onChange={(event) => setValidUntil(event.target.value)}
            />
          </Field>
        </div>

        {grant.isError && (
          <p
            role="alert"
            className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
          >
            {mandateError(grant.error, t)}
          </p>
        )}
      </form>
    </Modal>
  );
}

// RevokeMandateDialog ends one mandate, immediately or on a future date. The
// cascade is named before it happens: a delegate cannot outlive the authority it
// was cut from, so revoking a mandate ends everything cut from it too.
function RevokeMandateDialog({
  slug,
  mandate,
  cascade,
  onClose,
}: {
  slug: string;
  mandate: Mandate;
  cascade: Mandate[];
  onClose: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const revoke = useRevokeMandateMutation(slug);

  const [dated, setDated] = useState(false);
  const [effectiveAt, setEffectiveAt] = useState("");
  const [reason, setReason] = useState("");
  const [attempted, setAttempted] = useState(false);

  const effective = dated ? isoFromLocalInput(effectiveAt) : undefined;
  const effectiveMissing = attempted && dated && effective === undefined;
  // The backend refuses a date that has already passed, and revoking now is a
  // separate, explicit choice — so say that here rather than round-tripping into
  // its untranslated prose.
  const effectivePast =
    effective !== undefined && effective <= new Date().toISOString();

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (
      revoke.isPending ||
      effectivePast ||
      (dated && effective === undefined)
    ) {
      return;
    }
    revoke.mutate(
      {
        mandateId: mandate.id,
        effectiveAt: effective,
        reason: reason.trim() === "" ? undefined : reason.trim(),
      },
      { onSuccess: onClose },
    );
  }

  const formId = "mandate-revoke-form";

  return (
    <Modal
      title={t("mandates.revokeTitle")}
      closeLabel={t("common.cancel")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            type="submit"
            form={formId}
            size="sm"
            variant="danger"
            loading={revoke.isPending}
          >
            {t("mandates.revoke")}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        onSubmit={handleSubmit}
        noValidate
        className="flex flex-col gap-4"
      >
        <p className="text-ink-soft text-[13px]">
          {t("mandates.revokeIntro", {
            holder: mandate.granteeName,
            type: mandateTypeLabel(mandate.type, t),
          })}
        </p>

        {cascade.length > 0 && (
          <div className="bg-warning-bg text-warning-fg rounded-yivi flex items-start gap-2.5 p-3 text-[12.5px]">
            <Icon name="warning" size={16} className="mt-0.5 shrink-0" />
            <div>
              <p>{t("mandates.cascadeWarning", { count: cascade.length })}</p>
              <p className="mt-1 font-semibold">
                {cascade
                  .map((reached) => reached.granteeName)
                  .join(t("mandates.cascadeSeparator"))}
              </p>
            </div>
          </div>
        )}

        <fieldset className="border-0 p-0">
          <legend className={FIELD_LABEL}>{t("mandates.form.when")}</legend>
          <div className="mt-2 flex flex-col gap-2">
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="radio"
                name="mandate-revoke-when"
                checked={!dated}
                onChange={() => setDated(false)}
              />
              <span className="text-ink">{t("mandates.form.immediately")}</span>
            </label>
            <label className="flex items-center gap-2 text-[13px]">
              <input
                type="radio"
                name="mandate-revoke-when"
                checked={dated}
                onChange={() => setDated(true)}
              />
              <span className="text-ink">{t("mandates.form.onADate")}</span>
            </label>
          </div>
        </fieldset>

        {dated && (
          <Field
            id="mandate-effective-at"
            label={t("mandates.form.effectiveAt")}
            hint={t("mandates.form.effectiveAtHint")}
            error={
              effectiveMissing
                ? t("mandates.form.effectiveAtRequired")
                : effectivePast
                  ? t("mandates.form.effectiveAtPast")
                  : undefined
            }
          >
            <input
              id="mandate-effective-at"
              type="datetime-local"
              className={control(effectiveMissing || effectivePast)}
              value={effectiveAt}
              onChange={(event) => setEffectiveAt(event.target.value)}
            />
          </Field>
        )}

        <Field
          id="mandate-reason"
          label={t("mandates.form.reason")}
          hint={t("mandates.form.reasonHint")}
        >
          <input
            id="mandate-reason"
            className={control(false)}
            value={reason}
            maxLength={MAX_REVOCATION_REASON_LENGTH}
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>

        {revoke.isError && (
          <p
            role="alert"
            className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
          >
            {mandateError(revoke.error, t)}
          </p>
        )}
      </form>
    </Modal>
  );
}

export function MandateSettings({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const mandates = useMandatesQuery(slug);
  const authority = useMandateAuthorityQuery(slug);
  const formatDate = useDateFormatter();

  const [granting, setGranting] = useState(false);
  const [revoking, setRevoking] = useState<string | null>(null);

  const register = mandates.data ?? [];
  const rows = mandateLineage(register);
  const byId = new Map(register.map((mandate) => [mandate.id, mandate]));
  const availability = authority.data
    ? mandateGrantAvailability(authority.data)
    : "noAuthority";
  const target = revoking === null ? undefined : byId.get(revoking);

  const validity = (mandate: Mandate): string =>
    mandate.validUntil === null
      ? t("mandates.validityFrom", { from: formatDate(mandate.validFrom) })
      : t("mandates.validityRange", {
          from: formatDate(mandate.validFrom),
          until: formatDate(mandate.validUntil),
        });

  return (
    <Card className="overflow-hidden">
      <div className="flex items-start justify-between gap-4 p-7 pb-5">
        <div>
          <h2 className="text-[16px] font-semibold">{t("mandates.heading")}</h2>
          <p className="text-ink-soft mt-1 max-w-2xl text-[13px]">
            {t("mandates.description")}
          </p>
        </div>
        {availability === "available" && (
          <Button icon="add" onClick={() => setGranting(true)}>
            {t("mandates.grant")}
          </Button>
        )}
      </div>

      {availability !== "available" &&
        !authority.isPending &&
        !authority.isError && (
          <div className="px-7 pb-5">
            <Card variant="highlight" className="flex items-start gap-2.5 p-4">
              <Icon
                name="info"
                size={16}
                className="text-link mt-0.5 shrink-0"
              />
              <p className="text-ink text-[13px]">
                {availability === "jointAuthority"
                  ? t("mandates.jointAuthorityNote")
                  : t("mandates.noAuthorityNote")}
              </p>
            </Card>
          </div>
        )}

      <Table>
        <Table.Head>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.holder")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.grantedBy")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.type")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.scope")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.validity")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col">
            {t("mandates.columns.status")}
          </Table.HeaderCell>
          <Table.HeaderCell scope="col" srOnly>
            {t("mandates.columns.actions")}
          </Table.HeaderCell>
        </Table.Head>
        <Table.Body>
          {mandates.isError ? (
            <Table.State colSpan={COLUMN_COUNT}>
              <span className="text-error">
                {registerLoadError(mandates.error, t)}
              </span>
            </Table.State>
          ) : mandates.isPending ? (
            <Table.State colSpan={COLUMN_COUNT}>
              {t("common.loading")}
            </Table.State>
          ) : rows.length === 0 ? (
            <Table.State colSpan={COLUMN_COUNT}>
              {t("mandates.empty")}
            </Table.State>
          ) : (
            rows.map(({ mandate, depth }) => {
              const parentId = mandate.parentMandateId;
              const parent = parentId === null ? undefined : byId.get(parentId);
              return (
                <Table.Row key={mandate.id}>
                  <Table.Cell scope="row">
                    <div style={{ paddingLeft: depth * LINEAGE_INDENT_PX }}>
                      <span className="text-ink">{mandate.granteeName}</span>
                      {parent && (
                        <span className="text-muted block text-[12px]">
                          {t("mandates.delegatedFrom", {
                            holder: parent.granteeName,
                          })}
                        </span>
                      )}
                    </div>
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft">
                    {mandate.grantorName ?? t("mandates.grantorGone")}
                  </Table.Cell>
                  <Table.Cell>{mandateTypeLabel(mandate.type, t)}</Table.Cell>
                  <Table.Cell className="text-ink-soft">
                    {mandateScopeLabel(mandate, t)}
                  </Table.Cell>
                  <Table.Cell className="text-ink-soft text-[12.5px]">
                    {validity(mandate)}
                  </Table.Cell>
                  <Table.Cell>
                    <Tag tone={mandateStatusTone(mandate.status)} dot>
                      {mandateStatusLabel(mandate.status, t)}
                    </Tag>
                  </Table.Cell>
                  <Table.Cell className="text-right">
                    {availability === "available" &&
                      mandateIsRevocable(mandate) && (
                        <Button
                          size="sm"
                          variant="danger"
                          onClick={() => setRevoking(mandate.id)}
                        >
                          {t("mandates.revoke")}
                        </Button>
                      )}
                  </Table.Cell>
                </Table.Row>
              );
            })
          )}
        </Table.Body>
      </Table>

      {granting && (
        <GrantMandateDialog slug={slug} onClose={() => setGranting(false)} />
      )}
      {target && (
        <RevokeMandateDialog
          slug={slug}
          mandate={target}
          cascade={mandateCascade(register, target.id)}
          onClose={() => setRevoking(null)}
        />
      )}
    </Card>
  );
}
