import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useInviteMemberMutation,
  useOrganizationDepartmentsQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Button, Card, Icon, Tag, TopBar } from "../ui";
import * as React from "react";

const INVITE_MODES = [
  {
    key: "email",
    labelKey: "memberInvite.modeEmail",
    icon: "email",
    enabled: true,
  },
  {
    key: "link",
    labelKey: "memberInvite.modeLink",
    icon: "arrow_front",
    enabled: false,
  },
  {
    key: "bulk",
    labelKey: "memberInvite.modeBulk",
    icon: "add",
    enabled: false,
  },
] as const;

const FORM_ID = "member-invite-form";
const CONFLICT_STATUS = 409;
// Plausible-format check only; the backend is the authority (it just requires "@").
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const SUBHEAD = "text-ink text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:ring-3";
const CONTROL_OK = "border-line-strong focus:border-ink focus:ring-ink/10";
const CONTROL_ERR = "border-error focus:border-error focus:ring-error/10";

function control(hasError: boolean): string {
  return [CONTROL, hasError ? CONTROL_ERR : CONTROL_OK].join(" ");
}

type MessageKey =
  | "memberInvite.givenNamesRequired"
  | "memberInvite.lastNameRequired"
  | "memberInvite.emailRequired"
  | "memberInvite.emailInvalid";

type FieldErrors = {
  givenNames?: MessageKey;
  lastName?: MessageKey;
  email?: MessageKey;
};

type Values = {
  givenNames: string;
  lastName: string;
  email: string;
};

function validate(values: Values): FieldErrors {
  const errors: FieldErrors = {};
  if (values.givenNames.trim() === "") {
    errors.givenNames = "memberInvite.givenNamesRequired";
  }
  if (values.lastName.trim() === "") {
    errors.lastName = "memberInvite.lastNameRequired";
  }
  const email = values.email.trim();
  if (email === "") {
    errors.email = "memberInvite.emailRequired";
  } else if (!EMAIL_PATTERN.test(email)) {
    errors.email = "memberInvite.emailInvalid";
  }
  return errors;
}

function errorMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    return t("memberInvite.alreadyMember");
  }
  return t("memberInvite.error", { message: error.message });
}

// Trims a field and returns undefined when empty so optional values are omitted
// from the request body rather than sent as "".
function optional(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === "" ? undefined : trimmed;
}

function Field({
  id,
  label,
  required,
  error,
  className,
  children,
}: {
  id: string;
  label: string;
  required?: boolean;
  error?: string;
  className?: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <div className={["flex flex-col gap-1", className ?? ""].join(" ")}>
      <label htmlFor={id} className={FIELD_LABEL}>
        {label}
        {required && (
          <span aria-hidden className="text-error ml-0.5">
            *
          </span>
        )}
      </label>
      {children}
      {error && (
        <span
          id={`${id}-error`}
          role="alert"
          className="text-error text-[12px]"
        >
          {error}
        </span>
      )}
    </div>
  );
}

export default function MemberInvite(): React.JSX.Element | null {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const departments = useOrganizationDepartmentsQuery(slug, isAdmin);
  const invite = useInviteMemberMutation(slug);
  const [mode, setMode] = useState("email");
  const [givenNames, setGivenNames] = useState("");
  const [namePrefix, setNamePrefix] = useState("");
  const [lastName, setLastName] = useState("");
  const [preferredName, setPreferredName] = useState("");
  const [email, setEmail] = useState("");
  const [jobTitle, setJobTitle] = useState("");
  const [departmentId, setDepartmentId] = useState("");
  const [role, setRole] = useState("member");
  const [attempted, setAttempted] = useState(false);

  if (org.isPending) {
    return null;
  }
  // Inviting is admin-only; non-admins are bounced back to the members list.
  if (org.data?.role !== "admin") {
    return <Navigate to={`/${slug}/members`} replace />;
  }

  const orgName = org.data.name;
  const backToMembers = (): void => void navigate(`/${slug}/members`);

  // Errors surface only after the first submit attempt, then track edits live so
  // they clear as the user fixes each field.
  const errors = attempted ? validate({ givenNames, lastName, email }) : {};

  // TODO: this creates the member synchronously — no email is sent yet. The
  // email-preview, onboarding link, and "expires in 7 days" panels are an
  // aspirational mockup pending a real onboarding-email/token flow.
  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (invite.isPending) {
      return;
    }
    const found = validate({ givenNames, lastName, email });
    if (Object.keys(found).length > 0) {
      return;
    }
    invite.mutate(
      {
        email: email.trim(),
        givenNames: givenNames.trim(),
        lastName: lastName.trim(),
        preferredName: optional(preferredName),
        namePrefix: optional(namePrefix),
        role,
        jobTitle: optional(jobTitle),
        departmentId: departmentId === "" ? undefined : departmentId,
      },
      { onSuccess: backToMembers },
    );
  }

  return (
    <>
      <TopBar
        title={t("memberInvite.title")}
        subtitle={t("memberInvite.subtitle")}
        actions={
          <>
            <Button variant="secondary" onClick={backToMembers}>
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form={FORM_ID}
              icon="email"
              disabled={invite.isPending}
            >
              {invite.isPending
                ? t("memberInvite.sending")
                : t("memberInvite.send")}
            </Button>
          </>
        }
      />

      <form
        id={FORM_ID}
        onSubmit={handleSubmit}
        noValidate
        className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_340px]"
      >
        <div className="flex flex-col gap-4">
          <Card className="p-5">
            <div className={EYEBROW}>{t("memberInvite.howTo")}</div>
            <div className="mt-2.5 flex gap-2.5">
              {INVITE_MODES.map((m) => {
                const active = mode === m.key;
                return (
                  <button
                    key={m.key}
                    type="button"
                    disabled={!m.enabled}
                    aria-disabled={!m.enabled}
                    title={m.enabled ? undefined : t("common.comingSoon")}
                    onClick={() => m.enabled && setMode(m.key)}
                    className={[
                      "rounded-yivi flex flex-1 items-center gap-2.5 border px-3 py-3.5 text-[13.5px] font-semibold transition-colors",
                      !m.enabled
                        ? "bg-surface border-line-strong text-muted cursor-not-allowed opacity-60"
                        : active
                          ? "bg-highlight border-highlight-border text-link"
                          : "bg-surface border-line-strong text-ink hover:bg-surface-3",
                    ].join(" ")}
                  >
                    <Icon name={m.icon} size={16} />
                    {t(m.labelKey)}
                  </button>
                );
              })}
            </div>
          </Card>

          <Card className="p-5">
            <div className={EYEBROW}>{t("memberInvite.recipient")}</div>
            <div className="mt-3 flex flex-col gap-5">
              <div className="flex flex-col gap-2">
                <div className={SUBHEAD}>{t("common.name")}</div>
                <div className="grid grid-cols-12 gap-3">
                  <Field
                    id="invite-given-names"
                    label={t("memberInvite.givenNames")}
                    required
                    error={errors.givenNames && t(errors.givenNames)}
                    className="col-span-5"
                  >
                    <input
                      id="invite-given-names"
                      className={control(Boolean(errors.givenNames))}
                      value={givenNames}
                      onChange={(e) => setGivenNames(e.target.value)}
                      aria-required
                      aria-invalid={errors.givenNames ? true : undefined}
                      aria-describedby={
                        errors.givenNames
                          ? "invite-given-names-error"
                          : undefined
                      }
                      autoFocus
                    />
                  </Field>
                  <Field
                    id="invite-prefix"
                    label={t("memberInvite.prefix")}
                    className="col-span-3"
                  >
                    <input
                      id="invite-prefix"
                      className={control(false)}
                      value={namePrefix}
                      onChange={(e) => setNamePrefix(e.target.value)}
                    />
                  </Field>
                  <Field
                    id="invite-last-name"
                    label={t("memberInvite.lastName")}
                    required
                    error={errors.lastName && t(errors.lastName)}
                    className="col-span-4"
                  >
                    <input
                      id="invite-last-name"
                      className={control(Boolean(errors.lastName))}
                      value={lastName}
                      onChange={(e) => setLastName(e.target.value)}
                      aria-required
                      aria-invalid={errors.lastName ? true : undefined}
                      aria-describedby={
                        errors.lastName ? "invite-last-name-error" : undefined
                      }
                    />
                  </Field>
                  <Field
                    id="invite-preferred-name"
                    label={t("memberInvite.preferredName")}
                    className="col-span-12"
                  >
                    <input
                      id="invite-preferred-name"
                      className={control(false)}
                      value={preferredName}
                      onChange={(e) => setPreferredName(e.target.value)}
                    />
                  </Field>
                </div>
              </div>

              <div className="flex flex-col gap-2">
                <div className={SUBHEAD}>{t("memberInvite.groupContact")}</div>
                <Field
                  id="invite-email"
                  label={t("memberInvite.email")}
                  required
                  error={errors.email && t(errors.email)}
                >
                  <input
                    id="invite-email"
                    className={control(Boolean(errors.email))}
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    aria-required
                    aria-invalid={errors.email ? true : undefined}
                    aria-describedby={
                      errors.email ? "invite-email-error" : undefined
                    }
                  />
                </Field>
              </div>

              <div className="flex flex-col gap-2">
                <div className={SUBHEAD}>{t("memberInvite.groupRoleDept")}</div>
                <div className="grid grid-cols-3 gap-3">
                  <Field id="invite-role" label={t("common.role")}>
                    <select
                      id="invite-role"
                      className={control(false)}
                      value={role}
                      onChange={(e) => setRole(e.target.value)}
                    >
                      <option value="member">
                        {t("memberInvite.roleMember")}
                      </option>
                      <option value="admin">
                        {t("memberInvite.roleAdmin")}
                      </option>
                    </select>
                  </Field>
                  <Field id="invite-department" label={t("common.department")}>
                    <select
                      id="invite-department"
                      className={control(false)}
                      value={departmentId}
                      onChange={(e) => setDepartmentId(e.target.value)}
                    >
                      <option value="">
                        {t("memberInvite.selectDepartment")}
                      </option>
                      {departments.data?.map((d) => (
                        <option key={d.id} value={d.id}>
                          {d.name}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field id="invite-job-title" label={t("common.jobTitle")}>
                    <input
                      id="invite-job-title"
                      className={control(false)}
                      value={jobTitle}
                      onChange={(e) => setJobTitle(e.target.value)}
                    />
                  </Field>
                </div>
              </div>
            </div>

            {invite.isError && (
              <p
                role="alert"
                className="rounded-yivi bg-error-bg text-error mt-3 px-3 py-2 text-[13px]"
              >
                {errorMessage(invite.error, t)}
              </p>
            )}
          </Card>

          <Card className="p-5">
            <div className="flex items-center justify-between">
              <div className={EYEBROW}>{t("memberInvite.attestations")}</div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                icon="add"
                disabled
                title={t("common.comingSoon")}
              >
                {t("memberInvite.addMore")}
              </Button>
            </div>
            <div className="mt-2.5 flex flex-wrap gap-2">
              <Tag tone="blue">
                {t("memberInvite.attMemberOf", { org: orgName })}
              </Tag>
              <Tag tone="blue">{t("memberInvite.attCorporateEmail")}</Tag>
            </div>
          </Card>
        </div>

        <div className="flex flex-col gap-4">
          <Card className="p-5">
            <div className={EYEBROW}>{t("memberInvite.previewEmail")}</div>
            <div className="border-line bg-surface-2 mt-2.5 rounded-md border p-3.5 text-[13px] leading-relaxed">
              <div className="mb-2 font-semibold">
                {t("memberInvite.previewTitle", { org: orgName })}
              </div>
              <div className="text-ink-soft">
                <p>{t("memberInvite.previewGreeting")}</p>
                <p className="mt-2">
                  {t("memberInvite.previewBody", { org: orgName })}
                </p>
                <p className="mt-2 font-mono text-[11px]">
                  https://{slug}.yivi.app/onboard/…
                </p>
                <p className="mt-2">{t("memberInvite.previewExpiry")}</p>
              </div>
            </div>
          </Card>

          <Card variant="highlight" className="p-4">
            <div className="flex items-start gap-2.5">
              <Icon
                name="info"
                size={16}
                className="text-link mt-0.5 shrink-0"
              />
              <p className="text-ink text-[13px]">
                {t("memberInvite.appNote")}
              </p>
            </div>
          </Card>
        </div>
      </form>
    </>
  );
}
