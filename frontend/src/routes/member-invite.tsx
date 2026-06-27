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
  { key: "email", labelKey: "memberInvite.modeEmail", icon: "email" },
  { key: "link", labelKey: "memberInvite.modeLink", icon: "arrow_front" },
  { key: "bulk", labelKey: "memberInvite.modeBulk", icon: "add" },
] as const;

const CONFLICT_STATUS = 409;

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

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
  label,
  wide,
  children,
}: {
  label: string;
  wide?: boolean;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <label
      className={["flex flex-col gap-1", wide ? "col-span-2" : ""].join(" ")}
    >
      <span className={FIELD_LABEL}>{label}</span>
      {children}
    </label>
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

  if (org.isPending) {
    return null;
  }
  // Inviting is admin-only; non-admins are bounced back to the members list.
  if (org.data?.role !== "admin") {
    return <Navigate to={`/${slug}/members`} replace />;
  }

  const orgName = org.data.name;
  const backToMembers = (): void => void navigate(`/${slug}/members`);

  const canSubmit =
    givenNames.trim() !== "" &&
    lastName.trim() !== "" &&
    email.trim() !== "" &&
    !invite.isPending;

  // TODO: this creates the member synchronously — no email is sent yet. The
  // email-preview, onboarding link, and "expires in 7 days" panels are an
  // aspirational mockup pending a real onboarding-email/token flow.
  function handleSubmit(): void {
    if (!canSubmit) {
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
              {t("memberInvite.cancel")}
            </Button>
            <Button icon="email" onClick={handleSubmit} disabled={!canSubmit}>
              {invite.isPending
                ? t("memberInvite.sending")
                : t("memberInvite.send")}
            </Button>
          </>
        }
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_340px]">
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
                    onClick={() => setMode(m.key)}
                    className={[
                      "rounded-yivi flex flex-1 items-center gap-2.5 border px-3 py-3.5 text-[13.5px] font-semibold transition-colors",
                      active
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
            <div className="mt-2.5 grid grid-cols-2 gap-3">
              <Field label={t("memberInvite.givenNames")}>
                <input
                  className={CONTROL}
                  value={givenNames}
                  onChange={(e) => setGivenNames(e.target.value)}
                  autoFocus
                />
              </Field>
              <Field label={t("memberInvite.prefix")}>
                <input
                  className={CONTROL}
                  value={namePrefix}
                  onChange={(e) => setNamePrefix(e.target.value)}
                />
              </Field>
              <Field label={t("memberInvite.lastName")}>
                <input
                  className={CONTROL}
                  value={lastName}
                  onChange={(e) => setLastName(e.target.value)}
                />
              </Field>
              <Field label={t("memberInvite.preferredName")}>
                <input
                  className={CONTROL}
                  value={preferredName}
                  onChange={(e) => setPreferredName(e.target.value)}
                />
              </Field>
              <Field label={t("memberInvite.email")} wide>
                <input
                  className={CONTROL}
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </Field>
              <Field label={t("memberInvite.jobTitle")}>
                <input
                  className={CONTROL}
                  value={jobTitle}
                  onChange={(e) => setJobTitle(e.target.value)}
                />
              </Field>
              <Field label={t("memberInvite.department")}>
                <select
                  className={CONTROL}
                  value={departmentId}
                  onChange={(e) => setDepartmentId(e.target.value)}
                >
                  <option value="">{t("memberInvite.selectDepartment")}</option>
                  {departments.data?.map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label={t("memberInvite.role")}>
                <select
                  className={CONTROL}
                  value={role}
                  onChange={(e) => setRole(e.target.value)}
                >
                  <option value="member">{t("memberInvite.roleMember")}</option>
                  <option value="admin">{t("memberInvite.roleAdmin")}</option>
                </select>
              </Field>
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
              <Button variant="ghost" size="sm" icon="add">
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
      </div>
    </>
  );
}
