import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { Department, Member } from "../api/organization";
import {
  useOrganizationDepartmentsQuery,
  useOrganizationMembersQuery,
  useOrganizationQuery,
  useUpdateMemberMutation,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { fullName } from "../lib/name";
import { ApiError } from "../api/http";
import { Button, Card, TopBar } from "../ui";
import * as React from "react";

const FORM_ID = "member-edit-form";
const CONFLICT_STATUS = 409;
const FIELD_LABEL = "text-ink text-[13px] font-semibold";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

function EditForm({
  slug,
  member,
  departments,
}: {
  slug: string;
  member: Member;
  departments: Department[];
}): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const update = useUpdateMemberMutation(slug);
  const [role, setRole] = useState(member.role);
  const [jobTitle, setJobTitle] = useState(member.jobTitle ?? "");
  const [departmentId, setDepartmentId] = useState(member.departmentId ?? "");

  const backToMember = (): void =>
    void navigate(`/${slug}/members/${member.userId}`);

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    update.mutate(
      {
        userId: member.userId,
        role,
        jobTitle: jobTitle.trim() === "" ? null : jobTitle.trim(),
        departmentId: departmentId === "" ? null : departmentId,
      },
      { onSuccess: backToMember },
    );
  }

  const errorText =
    update.error instanceof ApiError && update.error.status === CONFLICT_STATUS
      ? t("memberEdit.lastAdmin")
      : t("common.saveError", { message: update.error?.message ?? "" });

  return (
    <>
      <TopBar
        title={t("memberEdit.title")}
        subtitle={fullName(member)}
        actions={
          <>
            <Button variant="secondary" onClick={backToMember}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" form={FORM_ID} disabled={update.isPending}>
              {update.isPending ? t("common.saving") : t("common.save")}
            </Button>
          </>
        }
      />

      <div className="p-8">
        <Card className="max-w-lg p-6">
          <form
            id={FORM_ID}
            onSubmit={handleSubmit}
            className="flex flex-col gap-4"
          >
            <label className="flex flex-col gap-1.5">
              <span className={FIELD_LABEL}>{t("common.role")}</span>
              <select
                className={CONTROL}
                value={role}
                onChange={(event) => setRole(event.target.value)}
              >
                <option value="member">{t("memberInvite.roleMember")}</option>
                <option value="admin">{t("memberInvite.roleAdmin")}</option>
              </select>
            </label>

            <label className="flex flex-col gap-1.5">
              <span className={FIELD_LABEL}>{t("common.jobTitle")}</span>
              <input
                className={CONTROL}
                value={jobTitle}
                onChange={(event) => setJobTitle(event.target.value)}
                placeholder={t("memberEdit.jobTitlePlaceholder")}
                autoFocus
              />
            </label>

            <label className="flex flex-col gap-1.5">
              <span className={FIELD_LABEL}>{t("common.department")}</span>
              <select
                className={CONTROL}
                value={departmentId}
                onChange={(event) => setDepartmentId(event.target.value)}
              >
                <option value="">{t("memberEdit.noDepartment")}</option>
                {departments.map((department) => (
                  <option key={department.id} value={department.id}>
                    {department.name}
                  </option>
                ))}
              </select>
            </label>

            {update.isError && (
              <p
                role="alert"
                className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
              >
                {errorText}
              </p>
            )}
          </form>
        </Card>
      </div>
    </>
  );
}

export default function MemberEdit(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, userId } = useParams();
  // Both are guaranteed by the ":orgSlug/members/:userId/edit" route.
  const slug = orgSlug!;
  const id = userId!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);
  const departments = useOrganizationDepartmentsQuery(slug, isAdmin);
  const member = members.data?.find((m) => m.userId === id);

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={t("memberEdit.title")} />
      <div className="p-8">{body}</div>
    </>
  );
  const message = (text: string, isError = false): React.JSX.Element => (
    <Card className="p-6">
      <p className={`text-[14px] ${isError ? "text-error" : "text-ink-soft"}`}>
        {text}
      </p>
    </Card>
  );

  if (org.isError) {
    return shell(message(accessMessage(org.error, t), true));
  }
  if (org.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!isAdmin) {
    return shell(message(t("members.adminOnly")));
  }
  if (members.isError) {
    return shell(message(accessMessage(members.error, t), true));
  }
  if (members.isPending || departments.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!member) {
    return shell(message(t("memberDetail.notFound")));
  }

  return (
    <EditForm
      slug={slug}
      member={member}
      departments={departments.data ?? []}
    />
  );
}
