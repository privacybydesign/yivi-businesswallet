import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationsQuery } from "../api/organization.queries";
import { Button, Card, Stat, TopBar } from "../ui";
import * as React from "react";

export default function AdminDashboard(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const organizations = useOrganizationsQuery();
  const orgCount = organizations.data?.length ?? 0;

  return (
    <>
      <TopBar
        title={t("adminDashboard.title")}
        subtitle={t("adminDashboard.subtitle")}
      />

      <div className="flex flex-col gap-6 p-8">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Stat
            label={t("adminDashboard.stats.organizations")}
            value={organizations.isPending ? "—" : orgCount}
            hint={t("adminDashboard.stats.organizationsHint")}
            icon="personal"
          />
        </div>

        <Card variant="highlight" className="p-6">
          <h2 className="text-[18px] font-semibold">
            {t("adminDashboard.title")}
          </h2>
          <p className="text-ink-soft mt-1 max-w-xl text-[14px]">
            {t("adminDashboard.subtitle")}
          </p>
          <div className="mt-4 flex gap-2">
            <Button
              variant="secondary"
              icon="arrow_front"
              onClick={() => void navigate("/admin/organizations")}
            >
              {t("adminDashboard.viewAll")}
            </Button>
            <Button
              icon="add"
              onClick={() => void navigate("/admin/organizations/new")}
            >
              {t("adminDashboard.create")}
            </Button>
          </div>
        </Card>
      </div>
    </>
  );
}
