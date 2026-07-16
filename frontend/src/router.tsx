import { createBrowserRouter } from "react-router";
import type { CrumbContext, RouteHandle } from "./ui";
import type { Member, OrganizationDetail } from "./api/organization";
import type { QerdsMessageWithEvidence } from "./api/qerds";
import {
  organizationMemberQueryKey,
  organizationQueryKey,
} from "./api/organization.queries";
import { qerdsMessageQueryKey } from "./api/qerds.queries";
import { fullName } from "./lib/name";
import Root from "./routes/root";
import RootRedirect from "./routes/root-redirect";
import ProtectedRoute from "./routes/protected-route";
import AdminRoute from "./routes/admin-route";
import Login from "./routes/login";
import Register from "./routes/register";
import InviteAccept from "./routes/invite-accept";
import MyInvitations from "./routes/my-invitations";
import Enroll from "./routes/enroll";
import IdentityReviews from "./routes/identity-reviews";
import Dashboard from "./routes/dashboard";
import Members from "./routes/members";
import MemberInvite from "./routes/member-invite";
import MemberDetail from "./routes/member-detail";
import MemberEdit from "./routes/member-edit";
import AuditLog from "./routes/audit-log";
import Qerds from "./routes/qerds";
import QerdsCompose from "./routes/qerds-compose";
import QerdsAddresses from "./routes/qerds-addresses";
import QerdsMessage from "./routes/qerds-message";
import Settings from "./routes/settings";
import AdminDashboard from "./routes/admin-dashboard";
import AllOrganizations from "./routes/all-organizations";
import NotFound from "./routes/not-found";
import ErrorBoundary from "./routes/error-boundary";

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
    const member = queryClient.getQueryData<Member>(
      organizationMemberQueryKey(params.orgSlug ?? "", params.userId ?? ""),
    );
    return member ? fullName(member) : t("memberDetail.title");
  },
};
const memberEditCrumb: RouteHandle = {
  crumb: ({ t }) => t("common.edit"),
};
const auditLogCrumb: RouteHandle = { crumb: ({ t }) => t("auditLog.title") };
const qerdsCrumb: RouteHandle = { crumb: ({ t }) => t("qerds.title") };
const qerdsComposeCrumb: RouteHandle = {
  crumb: ({ t }) => t("qerds.compose.title"),
};
const qerdsAddressesCrumb: RouteHandle = {
  crumb: ({ t }) => t("qerds.addresses.title"),
};
const qerdsMessageCrumb: RouteHandle = {
  crumb: ({ params, queryClient, t }: CrumbContext) => {
    const message = queryClient.getQueryData<QerdsMessageWithEvidence>(
      qerdsMessageQueryKey(params.orgSlug ?? "", params.messageId ?? ""),
    );
    return message ? message.subject : t("qerds.message.title");
  },
};
const settingsCrumb: RouteHandle = { crumb: ({ t }) => t("settings.title") };
const invitationsCrumb: RouteHandle = {
  crumb: ({ t }) => t("myInvitations.title"),
};
const enrollCrumb: RouteHandle = { crumb: ({ t }) => t("enroll.title") };
const adminCrumb: RouteHandle = { crumb: ({ t }) => t("adminDashboard.title") };
const reviewsCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityReviews.title"),
};
const orgsCrumb: RouteHandle = {
  crumb: ({ t }) => t("allOrganizations.title"),
};

export const router = createBrowserRouter([
  {
    ErrorBoundary,
    children: [
      { path: "/login", Component: Login },
      { path: "/register", Component: Register },
      { path: "/invite/:token", Component: InviteAccept },
      { path: "*", Component: NotFound },
      {
        Component: ProtectedRoute,
        children: [
          { index: true, Component: RootRedirect },
          {
            Component: Root,
            children: [
              {
                path: "invitations",
                Component: MyInvitations,
                handle: invitationsCrumb,
              },
              {
                path: "enroll",
                Component: Enroll,
                handle: enrollCrumb,
              },
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
                  {
                    path: "qerds",
                    handle: qerdsCrumb,
                    children: [
                      { index: true, Component: Qerds },
                      {
                        path: "compose",
                        Component: QerdsCompose,
                        handle: qerdsComposeCrumb,
                      },
                      {
                        path: "addresses",
                        Component: QerdsAddresses,
                        handle: qerdsAddressesCrumb,
                      },
                      {
                        path: ":messageId",
                        Component: QerdsMessage,
                        handle: qerdsMessageCrumb,
                      },
                    ],
                  },
                  {
                    path: "audit-log",
                    Component: AuditLog,
                    handle: auditLogCrumb,
                  },
                  {
                    path: "settings",
                    Component: Settings,
                    handle: settingsCrumb,
                  },
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
                        path: "identity-reviews",
                        Component: IdentityReviews,
                        handle: reviewsCrumb,
                      },
                      {
                        path: "organizations",
                        handle: orgsCrumb,
                        children: [
                          { index: true, Component: AllOrganizations },
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
    ],
  },
]);
