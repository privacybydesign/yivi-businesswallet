import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
  useResendInvitationMutation,
  useRevokeInvitationMutation,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { fullName, personInitials } from "../lib/name";
import { Avatar, Button, Card, Tag, TopBar } from "../ui";
import * as React from "react";

export default function Members(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);
  const resend = useResendInvitationMutation(slug);
  const revoke = useRevokeInvitationMutation(slug);

  return (
    <>
      <TopBar
        title={t("members.title")}
        subtitle={
          org.isPending || (isAdmin && members.isPending)
            ? t("common.loading")
            : members.data
              ? t("members.count", { count: members.data.length })
              : t("members.subtitle")
        }
        actions={
          isAdmin ? (
            <Button
              icon="add"
              onClick={() => void navigate(`/${slug}/members/invite`)}
            >
              {t("members.invite")}
            </Button>
          ) : undefined
        }
      />

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : !org.isPending && !isAdmin ? (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">
              {t("members.adminOnly")}
            </p>
          </Card>
        ) : members.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("members.loadError", { message: members.error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <table className="w-full border-collapse text-[13.5px]">
              <thead>
                <tr>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("members.columnMember")}
                  </th>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("common.jobTitle")}
                  </th>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("common.department")}
                  </th>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("members.columnStatus")}
                  </th>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("common.role")}
                  </th>
                  <th className="border-line bg-surface-2 border-b px-3.5 py-2.5">
                    <span className="sr-only">
                      {t("members.columnActions")}
                    </span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {org.isPending || members.isPending ? (
                  <tr>
                    <td className="text-ink-soft px-3.5 py-3" colSpan={6}>
                      {t("common.loading")}
                    </td>
                  </tr>
                ) : (
                  members.data.map((member) => {
                    const pending = member.status === "invited";
                    return (
                      <tr
                        key={
                          member.userId ?? member.invitationId ?? member.email
                        }
                        onClick={
                          pending
                            ? undefined
                            : () =>
                                void navigate(
                                  `/${slug}/members/${member.userId}`,
                                )
                        }
                        className={
                          pending
                            ? ""
                            : "hover:bg-surface-3 cursor-pointer transition-colors"
                        }
                      >
                        <td className="border-line border-b px-3.5 py-3">
                          <div className="flex items-center gap-2.5">
                            <Avatar initials={personInitials(member)} />
                            <div className="min-w-0">
                              {pending ? (
                                <span className="text-ink truncate">
                                  {fullName(member)}
                                </span>
                              ) : (
                                <Link
                                  to={`/${slug}/members/${member.userId}`}
                                  className="text-ink truncate"
                                >
                                  {fullName(member)}
                                </Link>
                              )}
                              <div className="text-ink-soft truncate text-[12px]">
                                {member.email}
                              </div>
                            </div>
                          </div>
                        </td>
                        <td className="border-line text-ink-soft border-b px-3.5 py-3">
                          {member.jobTitle ?? t("members.unassigned")}
                        </td>
                        <td className="border-line text-ink-soft border-b px-3.5 py-3">
                          {member.departmentName ?? t("members.unassigned")}
                        </td>
                        <td className="border-line border-b px-3.5 py-3">
                          {pending ? (
                            <Tag tone="amber" dot>
                              {t("members.pending")}
                            </Tag>
                          ) : (
                            <Tag tone="green" dot>
                              {t("members.active")}
                            </Tag>
                          )}
                        </td>
                        <td className="border-line border-b px-3.5 py-3">
                          <Tag
                            tone={member.role === "admin" ? "blue" : "default"}
                          >
                            <span className="capitalize">{member.role}</span>
                          </Tag>
                        </td>
                        <td className="border-line border-b px-3.5 py-3 text-right">
                          {pending && member.invitationId && (
                            <div className="flex items-center justify-end gap-2">
                              <Button
                                size="sm"
                                variant="ghost"
                                icon="email"
                                disabled={resend.isPending}
                                onClick={() =>
                                  resend.mutate({
                                    invitationId: member.invitationId!,
                                  })
                                }
                              >
                                {t("members.resend")}
                              </Button>
                              <Button
                                size="sm"
                                variant="danger"
                                icon="delete"
                                disabled={revoke.isPending}
                                onClick={() =>
                                  revoke.mutate({
                                    invitationId: member.invitationId!,
                                  })
                                }
                              >
                                {t("members.revoke")}
                              </Button>
                            </div>
                          )}
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </Card>
        )}
      </div>
    </>
  );
}
