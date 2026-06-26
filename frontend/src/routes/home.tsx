import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useMeQuery } from "../api/auth.queries";
import { useMyOrganizationsQuery } from "../api/organization.queries";
import { Button, Card, Stat, TopBar } from "../ui";
import * as React from "react";

export default function Home(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data: me } = useMeQuery();
  const organizations = useMyOrganizationsQuery();

  const orgCount = organizations.data?.length ?? 0;

  return (
    <>
      <TopBar
        title={t("home.title")}
        subtitle={
          me ? t("home.welcome", { email: me.email }) : t("home.subtitle")
        }
      />

      <div className="flex flex-col gap-6 p-8">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Stat
            label={t("home.statOrganizations")}
            value={organizations.isPending ? "—" : orgCount}
            hint={t("home.statOrganizationsHint")}
            icon="personal"
          />
          <Stat
            label={t("home.statAttestations")}
            value="0"
            hint={t("home.comingSoon")}
            icon="valid"
          />
          <Stat
            label={t("home.statDocuments")}
            value="0"
            hint={t("home.comingSoon")}
            icon="edit"
          />
        </div>

        <Card variant="highlight" className="p-6">
          <h2 className="text-[18px] font-semibold">{t("home.manageTitle")}</h2>
          <p className="mt-1 max-w-xl text-[14px] text-ink-soft">
            {t("home.manageBody")}
          </p>
          <div className="mt-4">
            <Button
              variant="secondary"
              icon="arrow_front"
              onClick={() => void navigate("/organizations")}
            >
              {t("home.viewOrganizations")}
            </Button>
          </div>
        </Card>
      </div>
    </>
  );
}
