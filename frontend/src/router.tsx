import { Fragment } from "react";
import type { ComponentType } from "react";
import { createBrowserRouter } from "react-router";
import { organizationLoader, sessionLoader } from "./route-loaders";
import type { CrumbContext, RouteHandle } from "./ui";
import type { Member, OrganizationDetail } from "./api/organization";
import type { QerdsMessageWithEvidence } from "./api/qerds";
import type { HeldAttestationClaims } from "./api/attestations";
import type { ProofingFlow, ProofingRequest } from "./api/identity-proofing";
import { credentialDisplayName } from "./lib/credential-display";
import {
  organizationMemberQueryKey,
  organizationQueryKey,
} from "./api/organization.queries";
import { qerdsMessageQueryKey } from "./api/qerds.queries";
import { heldClaimsBaseQueryKey } from "./api/attestations.queries";
import {
  proofingFlowsQueryKey,
  proofingRequestQueryKey,
} from "./api/identity-proofing.queries";
import { personName } from "./lib/proofing-reviews";
import { HOSTED_PREVIEW_PATH } from "./lib/hosted-preview";
import { fullName } from "./lib/name";
import Root from "./routes/root";
import RootRedirect from "./routes/root-redirect";
import ProtectedRoute from "./routes/protected-route";
import AdminRoute from "./routes/admin-route";
import NotFound from "./routes/not-found";
import ErrorBoundary from "./routes/error-boundary";

// Each page is its own chunk, fetched when its route first matches, so a
// public page does not download the admin app.
function page(
  load: () => Promise<{ default: ComponentType }>,
): () => Promise<{ Component: ComponentType }> {
  return async () => ({ Component: (await load()).default });
}

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
const identityProofingCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofing.title"),
};
const identityProofingFlowsCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofingFlows.title"),
};
const identityProofingFlowCrumb: RouteHandle = {
  crumb: ({ params, queryClient, t }: CrumbContext) => {
    const flows = queryClient.getQueryData<ProofingFlow[]>(
      proofingFlowsQueryKey(params.orgSlug ?? ""),
    );
    return (
      flows?.find((flow) => flow.id === params.flowId)?.name ??
      t("identityProofingFlows.title")
    );
  },
};
const proofingPeopleCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofing.people.title"),
};
const proofingReviewsCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofing.reviews.title"),
};
const proofingReviewCrumb: RouteHandle = {
  crumb: ({ params, queryClient, t }: CrumbContext) => {
    const request = queryClient.getQueryData<ProofingRequest>(
      proofingRequestQueryKey(params.orgSlug ?? "", params.requestId ?? ""),
    );
    return request
      ? personName(request, t)
      : t("identityProofing.reviews.title");
  },
};
const developersCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofing.developers.title"),
};
const proofingVerifyCrumb: RouteHandle = {
  crumb: ({ t }) => t("identityProofing.onScreen.title"),
};
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
const qerdsContactsCrumb: RouteHandle = {
  crumb: ({ t }) => t("qerds.contacts.title"),
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
const attestationsCrumb: RouteHandle = {
  crumb: ({ t }) => t("attestations.title"),
};
// The credential's name once its detail is cached (in whichever language it was
// read), else a generic label.
const heldCredentialCrumb: RouteHandle = {
  crumb: ({ params, queryClient, t }: CrumbContext) => {
    const cached = queryClient
      .getQueriesData<HeldAttestationClaims>({
        queryKey: heldClaimsBaseQueryKey(
          params.orgSlug ?? "",
          params.heldId ?? "",
        ),
      })
      .map(([, data]) => data)
      .find((data) => data?.vct !== undefined);
    return cached
      ? cached.displayName || credentialDisplayName(cached.vct)
      : t("attestations.held.detail.title");
  },
};
const postguardCrumb: RouteHandle = { crumb: ({ t }) => t("postguard.title") };
const postguardSendCrumb: RouteHandle = {
  crumb: ({ t }) => t("postguard.send.title"),
};
const settingsCrumb: RouteHandle = { crumb: ({ t }) => t("settings.title") };
const signingCrumb: RouteHandle = { crumb: ({ t }) => t("signing.title") };
const credentialRequestsCrumb: RouteHandle = {
  crumb: ({ t }) => t("credentialRequests.title"),
};
const invitationsCrumb: RouteHandle = {
  crumb: ({ t }) => t("myInvitations.title"),
};
const profileCrumb: RouteHandle = { crumb: ({ t }) => t("profile.title") };
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
    // Renders nothing while the first route's chunk loads; without one the
    // router logs a missing-fallback warning.
    HydrateFallback: Fragment,
    children: [
      { path: "/login", lazy: page(() => import("./routes/login")) },
      { path: "/register", lazy: page(() => import("./routes/register")) },
      {
        path: "/invite/:token",
        lazy: page(() => import("./routes/invite-accept")),
      },
      {
        path: "/reidentify/:token",
        lazy: page(() => import("./routes/reidentify")),
      },
      { path: "/claim/:token", lazy: page(() => import("./routes/claim")) },
      // A member's VOG submission, keyed by the token from a request/reminder
      // mail or the dashboard banner - public like /claim, no sign-in needed.
      { path: "/vog/:token", lazy: page(() => import("./routes/vog-submit")) },
      // Inbound OpenID4VP: the address an external verifier redirects to, then
      // the transaction by its opaque id. Not under ProtectedRoute — it handles
      // the sign-in step itself so the id can ride through /login?returnTo=.
      { path: "/openid4vp", lazy: page(() => import("./routes/openid4vp")) },
      {
        path: "/openid4vp/:id",
        lazy: page(() => import("./routes/openid4vp-transaction")),
      },
      // An external signee has no account, so their signing page is public and keyed
      // by the one-time token from their invitation mail.
      {
        path: "/sign/:token",
        lazy: page(() => import("./routes/signing-external")),
      },
      // The built-in signing service's authorization page, where a signing
      // ceremony sends the signer's browser to approve in the Yivi app: public,
      // keyed by the authorization id, for a member and an external signee alike.
      {
        path: "/signing/authorize/:id",
        lazy: page(() => import("./routes/signing-authorize")),
      },
      // An external person verifying from a hosted link: public, keyed by
      // the link's token, on the subject's own device.
      { path: "/p/:token", lazy: page(() => import("./routes/proof")) },
      // The hosted page the branding settings frame, from the page they send.
      {
        path: HOSTED_PREVIEW_PATH,
        lazy: page(() => import("./routes/hosted-preview")),
      },
      { path: "/r/:token", lazy: page(() => import("./routes/review-link")) },
      { path: "*", Component: NotFound },
      {
        Component: ProtectedRoute,
        loader: sessionLoader,
        children: [
          { index: true, Component: RootRedirect },
          {
            Component: Root,
            children: [
              {
                path: "invitations",
                lazy: page(() => import("./routes/my-invitations")),
                handle: invitationsCrumb,
              },
              {
                path: "profile",
                lazy: page(() => import("./routes/profile")),
                handle: profileCrumb,
              },
              {
                path: "enroll",
                lazy: page(() => import("./routes/enroll")),
                handle: enrollCrumb,
              },
              {
                path: ":orgSlug",
                loader: organizationLoader,
                handle: orgCrumb,
                children: [
                  {
                    index: true,
                    lazy: page(() => import("./routes/dashboard")),
                  },
                  {
                    path: "identity-proofing",
                    handle: identityProofingCrumb,
                    children: [
                      {
                        index: true,
                        lazy: page(
                          () => import("./routes/identity-proofing-overview"),
                        ),
                      },
                      {
                        path: "flows",
                        handle: identityProofingFlowsCrumb,
                        children: [
                          {
                            index: true,
                            lazy: page(
                              () => import("./routes/identity-proofing-flows"),
                            ),
                          },
                          {
                            path: ":flowId",
                            lazy: page(
                              () => import("./routes/identity-proofing-flow"),
                            ),
                            handle: identityProofingFlowCrumb,
                          },
                        ],
                      },
                      {
                        path: "people",
                        lazy: page(() => import("./routes/proofing-people")),
                        handle: proofingPeopleCrumb,
                      },
                      {
                        path: "reviews",
                        handle: proofingReviewsCrumb,
                        children: [
                          {
                            index: true,
                            lazy: page(
                              () => import("./routes/proofing-reviews"),
                            ),
                          },
                          {
                            path: ":requestId",
                            lazy: page(
                              () => import("./routes/proofing-review"),
                            ),
                            handle: proofingReviewCrumb,
                          },
                        ],
                      },
                      {
                        path: "developers",
                        lazy: page(() => import("./routes/developers")),
                        handle: developersCrumb,
                      },
                      {
                        path: "verify",
                        lazy: page(() => import("./routes/proofing-verify")),
                        handle: proofingVerifyCrumb,
                      },
                    ],
                  },
                  {
                    path: "members",
                    handle: membersCrumb,
                    children: [
                      {
                        index: true,
                        lazy: page(() => import("./routes/members")),
                      },
                      {
                        path: "invite",
                        lazy: page(() => import("./routes/member-invite")),
                        handle: inviteCrumb,
                      },
                      {
                        path: ":userId",
                        handle: memberCrumb,
                        children: [
                          {
                            index: true,
                            lazy: page(() => import("./routes/member-detail")),
                          },
                          {
                            path: "edit",
                            lazy: page(() => import("./routes/member-edit")),
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
                      {
                        index: true,
                        lazy: page(() => import("./routes/qerds")),
                      },
                      {
                        path: "compose",
                        lazy: page(() => import("./routes/qerds-compose")),
                        handle: qerdsComposeCrumb,
                      },
                      {
                        path: "addresses",
                        lazy: page(() => import("./routes/qerds-addresses")),
                        handle: qerdsAddressesCrumb,
                      },
                      {
                        path: "contacts",
                        lazy: page(() => import("./routes/qerds-contacts")),
                        handle: qerdsContactsCrumb,
                      },
                      {
                        path: ":messageId",
                        lazy: page(() => import("./routes/qerds-message")),
                        handle: qerdsMessageCrumb,
                      },
                    ],
                  },
                  {
                    path: "attestations",
                    handle: attestationsCrumb,
                    children: [
                      {
                        index: true,
                        lazy: page(() => import("./routes/attestations")),
                      },
                      {
                        path: "held/:heldId",
                        lazy: page(() => import("./routes/attestations-held")),
                        handle: heldCredentialCrumb,
                      },
                    ],
                  },
                  {
                    path: "postguard",
                    handle: postguardCrumb,
                    children: [
                      {
                        index: true,
                        lazy: page(() => import("./routes/postguard")),
                      },
                      {
                        path: "send",
                        lazy: page(() => import("./routes/postguard-send")),
                        handle: postguardSendCrumb,
                      },
                    ],
                  },
                  {
                    path: "signing",
                    lazy: page(() => import("./routes/signing")),
                    handle: signingCrumb,
                  },
                  {
                    path: "credential-requests",
                    lazy: page(() => import("./routes/credential-requests")),
                    handle: credentialRequestsCrumb,
                  },
                  {
                    path: "audit-log",
                    lazy: page(() => import("./routes/audit-log")),
                    handle: auditLogCrumb,
                  },
                  {
                    path: "settings",
                    lazy: page(() => import("./routes/settings")),
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
                      {
                        index: true,
                        lazy: page(() => import("./routes/admin-dashboard")),
                      },
                      {
                        path: "identity-reviews",
                        lazy: page(() => import("./routes/identity-reviews")),
                        handle: reviewsCrumb,
                      },
                      {
                        path: "organizations",
                        handle: orgsCrumb,
                        children: [
                          {
                            index: true,
                            lazy: page(
                              () => import("./routes/all-organizations"),
                            ),
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
    ],
  },
]);
