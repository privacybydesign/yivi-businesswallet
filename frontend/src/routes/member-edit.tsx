import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { Department, Member, MemberType } from "../api/organization";
import {
  useOrganizationDepartmentsQuery,
  useOrganizationMemberQuery,
  useOrganizationQuery,
  useSetReviewRightMutation,
  useUpdateMemberMutation,
  useUpdateMemberTypeMutation,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { fullName } from "../lib/name";
import {
  reviewRightChanged,
  reviewRightLocked,
  reviewRightShown,
} from "../lib/review-right";
import { ApiError } from "../api/http";
import { Button, Card, Select, TopBar } from "../ui";
import * as React from "react";

const FORM_ID = "member-edit-form";
const CONFLICT_STATUS = 409;
const CAN_REVIEW_HINT_ID = "member-edit-can-review-hint";
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
  const updateType = useUpdateMemberTypeMutation(slug);
  const setReviewRight = useSetReviewRightMutation(slug);
  const [role, setRole] = useState(member.role);
  const [canReview, setCanReview] = useState(member.canReview);
  const [jobTitle, setJobTitle] = useState(member.jobTitle ?? "");
  const [departmentId, setDepartmentId] = useState(member.departmentId ?? "");
  const [memberType, setMemberType] = useState<MemberType>(
    member.memberType === "external" ? "external" : "employee",
  );
  const [externalOrganisation, setExternalOrganisation] = useState(
    member.externalOrganisation ?? "",
  );

  const backToMember = (): void =>
    void navigate(`/${slug}/members/${member.userId}`);

  const trimmedExternal = externalOrganisation.trim();
  const typeChanged =
    memberType !== member.memberType ||
    (memberType === "external" &&
      trimmedExternal !== (member.externalOrganisation ?? ""));

  const reviewChanged = reviewRightChanged(member, { role, canReview });

  // The membership fields, the member type and the review right are three
  // routes (changing the type recomputes the re-identification deadline
  // server-side), so a save that touches several chains them and only
  // navigates once the last lands.
  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    const saveReviewRight = (): void => {
      if (!reviewChanged) {
        backToMember();
        return;
      }
      setReviewRight.mutate(
        { userId: member.userId, canReview },
        { onSuccess: backToMember },
      );
    };
    const saveType = (): void => {
      if (!typeChanged) {
        saveReviewRight();
        return;
      }
      updateType.mutate(
        {
          userId: member.userId,
          memberType,
          externalOrganisation:
            memberType === "external" && trimmedExternal !== ""
              ? trimmedExternal
              : null,
        },
        { onSuccess: saveReviewRight },
      );
    };
    update.mutate(
      {
        userId: member.userId,
        role,
        jobTitle: jobTitle.trim() === "" ? null : jobTitle.trim(),
        departmentId: departmentId === "" ? null : departmentId,
      },
      { onSuccess: saveType },
    );
  }

  const saveError = update.error ?? updateType.error ?? setReviewRight.error;
  const errorText =
    update.error instanceof ApiError && update.error.status === CONFLICT_STATUS
      ? t("memberEdit.lastAdmin")
      : t("common.saveError", { message: saveError?.message ?? "" });
  const saving =
    update.isPending || updateType.isPending || setReviewRight.isPending;

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
            <Button type="submit" form={FORM_ID} disabled={saving}>
              {saving ? t("common.saving") : t("common.save")}
            </Button>
          </>
        }
      />

      <div className="p-4 sm:p-8">
        <Card className="max-w-lg p-6">
          <form
            id={FORM_ID}
            onSubmit={handleSubmit}
            className="flex flex-col gap-4"
          >
            <label className="flex flex-col gap-1.5">
              <span className={FIELD_LABEL}>{t("common.role")}</span>
              <Select
                value={role}
                onChange={(event) => setRole(event.target.value)}
              >
                <option value="member">{t("memberInvite.roleMember")}</option>
                <option value="admin">{t("memberInvite.roleAdmin")}</option>
              </Select>
            </label>

            <div className="flex flex-col gap-1.5">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={reviewRightShown({ role, canReview })}
                  disabled={reviewRightLocked(role)}
                  aria-describedby={CAN_REVIEW_HINT_ID}
                  onChange={(event) => setCanReview(event.target.checked)}
                />
                <span className={FIELD_LABEL}>{t("common.canReview")}</span>
              </label>
              <span
                id={CAN_REVIEW_HINT_ID}
                className="text-ink-soft text-[12px]"
              >
                {t("memberEdit.canReviewHint")}
              </span>
            </div>

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
              <Select
                value={departmentId}
                onChange={(event) => setDepartmentId(event.target.value)}
              >
                <option value="">{t("memberEdit.noDepartment")}</option>
                {departments.map((department) => (
                  <option key={department.id} value={department.id}>
                    {department.name}
                  </option>
                ))}
              </Select>
            </label>

            <label className="flex flex-col gap-1.5">
              <span className={FIELD_LABEL}>{t("memberEdit.memberType")}</span>
              <Select
                value={memberType}
                onChange={(event) =>
                  setMemberType(event.target.value as MemberType)
                }
              >
                <option value="employee">
                  {t("memberEdit.memberTypeEmployee")}
                </option>
                <option value="external">
                  {t("memberEdit.memberTypeExternal")}
                </option>
              </Select>
              <span className="text-ink-soft text-[12px]">
                {t("memberEdit.memberTypeHint")}
              </span>
            </label>

            {memberType === "external" && (
              <label className="flex flex-col gap-1.5">
                <span className={FIELD_LABEL}>
                  {t("memberEdit.externalOrganisation")}
                </span>
                <input
                  className={CONTROL}
                  value={externalOrganisation}
                  onChange={(event) =>
                    setExternalOrganisation(event.target.value)
                  }
                  placeholder={t("memberEdit.externalOrganisationPlaceholder")}
                />
              </label>
            )}

            {(update.isError ||
              updateType.isError ||
              setReviewRight.isError) && (
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
  const memberQuery = useOrganizationMemberQuery(slug, id, isAdmin);
  const departments = useOrganizationDepartmentsQuery(slug, isAdmin);
  const member = memberQuery.data;
  const notFound =
    memberQuery.error instanceof ApiError && memberQuery.error.status === 404;

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={t("memberEdit.title")} />
      <div className="p-4 sm:p-8">{body}</div>
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
  if (notFound) {
    return shell(message(t("memberDetail.notFound")));
  }
  if (memberQuery.isError) {
    return shell(message(accessMessage(memberQuery.error, t), true));
  }
  if (memberQuery.isPending || departments.isPending) {
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
