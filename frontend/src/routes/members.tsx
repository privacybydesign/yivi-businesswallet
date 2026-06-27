import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
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

  return (
    <>
      <TopBar
        title={t("members.title")}
        subtitle={org.data?.name ?? t("members.subtitle")}
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
              <tbody>
                {org.isPending || members.isPending ? (
                  <tr>
                    <td className="text-ink-soft px-6 py-3" colSpan={2}>
                      {t("common.loading")}
                    </td>
                  </tr>
                ) : (
                  members.data.map((member) => (
                    <tr key={member.userId}>
                      <td className="border-line border-b px-6 py-3">
                        <div className="flex items-center gap-2.5">
                          <Avatar initials={personInitials(member)} />
                          <div className="min-w-0">
                            <div className="text-ink truncate">
                              {fullName(member)}
                            </div>
                            <div className="text-ink-soft truncate text-[12px]">
                              {member.email}
                            </div>
                          </div>
                        </div>
                      </td>
                      <td className="border-line border-b px-6 py-3 text-right">
                        <Tag
                          tone={member.role === "admin" ? "blue" : "default"}
                        >
                          {member.role}
                        </Tag>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </Card>
        )}
      </div>
    </>
  );
}
