import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Avatar, Card, Tag, TopBar } from "../ui";
import * as React from "react";

const FORBIDDEN_STATUS = 403;
const NOT_FOUND_STATUS = 404;

function accessMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === FORBIDDEN_STATUS) {
    return t("dashboard.notMember");
  }
  if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
    return t("dashboard.notExist");
  }
  return error.message;
}

export default function Members(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  const slug = orgSlug ?? "";

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);

  return (
    <>
      <TopBar
        title={t("members.title")}
        subtitle={org.data?.name ?? t("members.subtitle")}
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
                          <Avatar
                            name={member.email.split("@")[0] ?? member.email}
                            tone="violet"
                          />
                          <span className="text-ink">{member.email}</span>
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
