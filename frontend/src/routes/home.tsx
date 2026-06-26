import { useNavigate } from "react-router";
import { useMeQuery } from "../api/auth.queries";
import { useMyOrganizationsQuery } from "../api/organization.queries";
import { Button, Card, Stat, TopBar } from "../ui";
import * as React from "react";

export default function Home(): React.JSX.Element {
  const navigate = useNavigate();
  const { data: me } = useMeQuery();
  const organizations = useMyOrganizationsQuery();

  const orgCount = organizations.data?.length ?? 0;

  return (
    <>
      <TopBar
        title="Dashboard"
        subtitle={
          me ? `Welcome back, ${me.email}` : "Your organizations at a glance"
        }
      />

      <div className="flex flex-col gap-6 p-8">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Stat
            label="Organizations"
            value={organizations.isPending ? "—" : orgCount}
            hint="Total managed"
            icon="personal"
          />
          <Stat
            label="Attestations issued"
            value="0"
            hint="Coming soon"
            icon="valid"
          />
          <Stat
            label="Documents to sign"
            value="0"
            hint="Coming soon"
            icon="edit"
          />
        </div>

        <Card variant="highlight" className="p-6">
          <h2 className="text-[18px] font-semibold">
            Manage your organizations
          </h2>
          <p className="mt-1 max-w-xl text-[14px] text-ink-soft">
            Review the organizations connected to your business wallet, their
            details, and the credentials they can issue.
          </p>
          <div className="mt-4">
            <Button
              variant="secondary"
              icon="arrow_front"
              onClick={() => void navigate("/organizations")}
            >
              View organizations
            </Button>
          </div>
        </Card>
      </div>
    </>
  );
}
