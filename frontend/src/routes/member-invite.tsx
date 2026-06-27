import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import { Button, Card, Icon, Tag, TopBar } from "../ui";
import * as React from "react";

const INVITE_MODES = [
  { key: "email", labelKey: "memberInvite.modeEmail", icon: "email" },
  { key: "link", labelKey: "memberInvite.modeLink", icon: "arrow_front" },
  { key: "bulk", labelKey: "memberInvite.modeBulk", icon: "add" },
] as const;

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const FIELD_LABEL = "text-ink-soft text-[12px] font-semibold";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

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
  const [mode, setMode] = useState("email");

  if (org.isPending) {
    return null;
  }
  // Inviting is admin-only; non-admins are bounced back to the members list.
  if (org.data?.role !== "admin") {
    return <Navigate to={`/${slug}/members`} replace />;
  }

  const orgName = org.data.name;
  const backToMembers = (): void => void navigate(`/${slug}/members`);

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
            <Button icon="email" onClick={backToMembers}>
              {t("memberInvite.send")}
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
                <input className={CONTROL} autoFocus />
              </Field>
              <Field label={t("memberInvite.prefix")}>
                <input className={CONTROL} />
              </Field>
              <Field label={t("memberInvite.lastName")}>
                <input className={CONTROL} />
              </Field>
              <Field label={t("memberInvite.preferredName")}>
                <input className={CONTROL} />
              </Field>
              <Field label={t("memberInvite.email")} wide>
                <input className={CONTROL} type="email" />
              </Field>
              <Field label={t("memberInvite.jobTitle")}>
                <input className={CONTROL} />
              </Field>
              <Field label={t("memberInvite.department")}>
                <select className={CONTROL} defaultValue="">
                  <option value="" disabled>
                    {t("memberInvite.selectDepartment")}
                  </option>
                </select>
              </Field>
              <Field label={t("memberInvite.role")}>
                <select className={CONTROL} defaultValue="member">
                  <option value="member">{t("memberInvite.roleMember")}</option>
                  <option value="admin">{t("memberInvite.roleAdmin")}</option>
                </select>
              </Field>
            </div>
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
