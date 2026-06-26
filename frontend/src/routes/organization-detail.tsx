import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Avatar, Card, Tag, TopBar } from "../ui";

const FORBIDDEN_STATUS = 403;
const NOT_FOUND_STATUS = 404;

function accessMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === FORBIDDEN_STATUS) {
    return t("organizationDetail.notMember");
  }
  if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
    return t("organizationDetail.notExist");
  }
  return error.message;
}

export default function OrganizationDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  const slug = orgSlug ?? "";

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);

  if (org.isError) {
    return (
      <>
        <TopBar title={slug} subtitle={t("organizationDetail.subtitle")} />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-[14px] text-error">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        </div>
      </>
    );
  }

  return (
    <>
      <TopBar
        title={org.data?.name ?? slug}
        subtitle={
          org.isPending
            ? t("common.loading")
            : t("organizationDetail.role", { role: org.data?.role ?? "" })
        }
        actions={
          org.data ? (
            <Tag tone={isAdmin ? "blue" : "default"}>{org.data.role}</Tag>
          ) : undefined
        }
      />

      <div className="flex flex-col gap-6 p-8">
        <Card className="p-6">
          <h2 className="text-[16px] font-semibold">
            {t("organizationDetail.details")}
          </h2>
          <dl className="mt-3 grid grid-cols-[120px_1fr] gap-y-2 text-[13.5px]">
            <dt className="text-muted">{t("organizationDetail.name")}</dt>
            <dd className="text-ink">{org.data?.name ?? "—"}</dd>
            <dt className="text-muted">{t("common.slug")}</dt>
            <dd className="font-mono text-ink-soft">{org.data?.slug ?? "—"}</dd>
            <dt className="text-muted">{t("organizationDetail.id")}</dt>
            <dd className="font-mono text-[12px] text-ink-soft">
              {org.data?.id ?? "—"}
            </dd>
          </dl>
        </Card>

        {isAdmin && (
          <Card className="overflow-hidden">
            <div className="border-b border-line px-6 py-4">
              <h2 className="text-[16px] font-semibold">
                {t("organizationDetail.members")}
              </h2>
            </div>
            {members.isError ? (
              <p className="px-6 py-4 text-[14px] text-error">
                {t("organizationDetail.membersLoadError", {
                  message: members.error.message,
                })}
              </p>
            ) : (
              <table className="w-full border-collapse text-[13.5px]">
                <tbody>
                  {members.isPending ? (
                    <tr>
                      <td className="px-6 py-3 text-ink-soft" colSpan={2}>
                        {t("common.loading")}
                      </td>
                    </tr>
                  ) : (
                    members.data.map((member) => (
                      <tr key={member.userId}>
                        <td className="border-b border-line px-6 py-3">
                          <div className="flex items-center gap-2.5">
                            <Avatar
                              name={member.email.split("@")[0] ?? member.email}
                              tone="violet"
                            />
                            <span className="text-ink">{member.email}</span>
                          </div>
                        </td>
                        <td className="border-b border-line px-6 py-3 text-right">
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
            )}
          </Card>
        )}
      </div>
    </>
  );
}
