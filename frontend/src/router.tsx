import { createBrowserRouter } from "react-router";
import type { CrumbContext, RouteHandle } from "./ui";
import type { Member, OrganizationDetail } from "./api/organization";
import {
  organizationMembersQueryKey,
  organizationQueryKey,
} from "./api/organization.queries";
import { fullName } from "./lib/name";
import Root from "./routes/root";
import RootRedirect from "./routes/root-redirect";
import ProtectedRoute from "./routes/protected-route";
import AdminRoute from "./routes/admin-route";
import Login from "./routes/login";
import Dashboard from "./routes/dashboard";
import Members from "./routes/members";
import MemberInvite from "./routes/member-invite";
import MemberDetail from "./routes/member-detail";
import MemberEdit from "./routes/member-edit";
import Settings from "./routes/settings";
import AdminDashboard from "./routes/admin-dashboard";
import AllOrganizations from "./routes/all-organizations";
import CreateOrganization from "./routes/create-organization";

// Breadcrumb labels live on the routes themselves; the trail is assembled
// automatically from the matched chain (see ui/breadcrumb.tsx).
const orgCrumb: RouteHandle = {
  crumb: ({ params, queryClient }: CrumbContext) => {
    const slug = params.orgSlug ?? "";
    const org = queryClient.getQueryData<OrganizationDetail>(
      organizationQueryKey(slug),
    );
    return org?.name ?? slug;
  },
};
const membersCrumb: RouteHandle = { crumb: ({ t }) => t("members.title") };
const inviteCrumb: RouteHandle = { crumb: ({ t }) => t("memberInvite.title") };
const memberCrumb: RouteHandle = {
  crumb: ({ params, queryClient, t }: CrumbContext) => {
    const members = queryClient.getQueryData<Member[]>(
      organizationMembersQueryKey(params.orgSlug ?? ""),
    );
    const member = members?.find((m) => m.userId === params.userId);
    return member ? fullName(member) : t("memberDetail.title");
  },
};
const memberEditCrumb: RouteHandle = {
  crumb: ({ t }) => t("common.edit"),
};
const settingsCrumb: RouteHandle = { crumb: ({ t }) => t("settings.title") };
const adminCrumb: RouteHandle = { crumb: ({ t }) => t("adminDashboard.title") };
const orgsCrumb: RouteHandle = {
  crumb: ({ t }) => t("allOrganizations.title"),
};
const newOrgCrumb: RouteHandle = { crumb: ({ t }) => t("createOrg.title") };

export const router = createBrowserRouter([
  { path: "/login", Component: Login },
  {
    Component: ProtectedRoute,
    children: [
      { index: true, Component: RootRedirect },
      {
        Component: Root,
        children: [
          {
            path: ":orgSlug",
            handle: orgCrumb,
            children: [
              { index: true, Component: Dashboard },
              {
                path: "members",
                handle: membersCrumb,
                children: [
                  { index: true, Component: Members },
                  {
                    path: "invite",
                    Component: MemberInvite,
                    handle: inviteCrumb,
                  },
                  {
                    path: ":userId",
                    handle: memberCrumb,
                    children: [
                      { index: true, Component: MemberDetail },
                      {
                        path: "edit",
                        Component: MemberEdit,
                        handle: memberEditCrumb,
                      },
                    ],
                  },
                ],
              },
              { path: "settings", Component: Settings, handle: settingsCrumb },
            ],
          },
          {
            Component: AdminRoute,
            children: [
              {
                path: "admin",
                handle: adminCrumb,
                children: [
                  { index: true, Component: AdminDashboard },
                  {
                    path: "organizations",
                    handle: orgsCrumb,
                    children: [
                      { index: true, Component: AllOrganizations },
                      {
                        path: "new",
                        Component: CreateOrganization,
                        handle: newOrgCrumb,
                      },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      },
    ],
  },
]);
