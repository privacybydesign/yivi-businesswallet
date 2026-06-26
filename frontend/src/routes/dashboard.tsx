import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Button, Card, Stat, Tag, TopBar } from "../ui";
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

export default function Dashboard(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  const slug = orgSlug ?? "";
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  if (org.isError) {
    return (
      <>
        <TopBar title={slug} />
        <div className="p-8">
          <Card className="p-6">
            <p className="text-error text-[14px]">
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
            : t("dashboard.role", { role: org.data?.role ?? "" })
        }
        actions={
          org.data ? (
            <Tag tone={isAdmin ? "blue" : "default"}>{org.data.role}</Tag>
          ) : undefined
        }
      />

      <div className="flex flex-col gap-6 p-8">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Stat
            label={t("dashboard.statAttestations")}
            value="0"
            hint={t("dashboard.comingSoon")}
            icon="valid"
          />
          <Stat
            label={t("dashboard.statDocuments")}
            value="0"
            hint={t("dashboard.comingSoon")}
            icon="edit"
          />
        </div>

        <Card className="p-6">
          <h2 className="text-[16px] font-semibold">
            {t("dashboard.details")}
          </h2>
          <dl className="mt-3 grid grid-cols-[120px_1fr] gap-y-2 text-[13.5px]">
            <dt className="text-muted">{t("dashboard.name")}</dt>
            <dd className="text-ink">{org.data?.name ?? "—"}</dd>
            <dt className="text-muted">{t("common.slug")}</dt>
            <dd className="text-ink-soft font-mono">{org.data?.slug ?? "—"}</dd>
            <dt className="text-muted">{t("dashboard.id")}</dt>
            <dd className="text-ink-soft font-mono text-[12px]">
              {org.data?.id ?? "—"}
            </dd>
          </dl>
          {isAdmin && (
            <div className="mt-4">
              <Button
                variant="secondary"
                icon="arrow_front"
                onClick={() => void navigate(`/${slug}/members`)}
              >
                {t("dashboard.viewMembers")}
              </Button>
            </div>
          )}
        </Card>
      </div>
    </>
  );
}
