import { Link, useParams } from "react-router";
import { Trans, useTranslation } from "react-i18next";
import { useMeQuery } from "../api/auth.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { useProofingPauseQuery } from "../api/identity-proofing.queries";
import { DashboardTodo, DashboardWaiting } from "./dashboard-todo";
import { IdentityBanner } from "./identity-banner";
import { VerifyPerson } from "./proofing-send";
import { VogBanner } from "./vog-banner";
import { accessMessage } from "../lib/access-message";
import { needsIdentityBanner } from "../lib/identity-status";
import { needsVogBanner } from "../lib/screening-status";
import { greetingKey } from "../lib/greeting";
import { displayName } from "../lib/name";
import { todoShows } from "../lib/proofing-todo";
import { Card, TopBar } from "../ui";
import * as React from "react";

export default function Dashboard(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const { data: me } = useMeQuery();
  const org = useOrganizationQuery(slug);
  const pause = useProofingPauseQuery(slug);
  const paused = pause.data?.paused === true;
  const isAdmin = org.data?.role === "admin";
  const canReview = org.data?.canReview === true;
  const shows = todoShows({ isAdmin, canReview, paused });
  const greeting = t(greetingKey(), { name: me ? displayName(me) : "" });

  if (org.isError) {
    return (
      <>
        <TopBar title={slug} />
        <div className="p-4 sm:p-8">
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
        title={greeting}
        actions={
          org.data && shows.verify ? (
            <VerifyPerson slug={slug} isAdmin={isAdmin} />
          ) : undefined
        }
      />

      <div className="flex max-w-240 flex-col gap-6 p-4 sm:p-8">
        {org.data && needsIdentityBanner(org.data.identity) && (
          <IdentityBanner
            slug={slug}
            orgName={org.data.name}
            identity={org.data.identity!}
          />
        )}
        {org.data && needsVogBanner(org.data.vog) && (
          <VogBanner slug={slug} orgName={org.data.name} vog={org.data.vog!} />
        )}
        {org.data && (
          <>
            <DashboardTodo
              slug={slug}
              orgName={org.data.name}
              isAdmin={isAdmin}
              canReview={shows.decisions}
            />
            <DashboardWaiting slug={slug} canReview={canReview} />
          </>
        )}
        {shows.orgDetails && (
          <p className="text-ink-soft text-[13px]">
            <Trans
              i18nKey="dashboard.orgDetails"
              components={{
                settings: (
                  <Link
                    to={`/${slug}/settings`}
                    className="text-link underline"
                  />
                ),
              }}
            />
          </p>
        )}
      </div>
    </>
  );
}
