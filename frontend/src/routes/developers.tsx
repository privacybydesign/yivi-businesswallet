import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import { absoluteApiUrl } from "../api/http";
import { buttonClassName, Card, TopBar } from "../ui";
import { ApiTokensSection } from "./developers-api-keys";
import { WebhookSection } from "./developers-webhook";

// The API reference the backend serves (internal/apidocs).
const API_DOCS_PATH = "/api/docs";
// Where the signing API takes a new signing request.
const SIGNING_API_PATH = "/api/v1/signing/requests";

// The API tokens are not only for identity proofing: the same ones open the
// signing API. Said once, under the tokens, with the way to its reference.
function SigningApiNote(): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Card className="px-5 py-4">
      <h2 className="font-display text-[17px] font-bold">
        {t("identityProofing.developers.signingApi.title")}
      </h2>
      <p className="text-ink-soft mt-1 text-[12.5px]">
        {t("identityProofing.developers.signingApi.note", {
          path: SIGNING_API_PATH,
        })}{" "}
        <a
          href={absoluteApiUrl(API_DOCS_PATH)}
          target="_blank"
          rel="noreferrer"
          className="text-link font-semibold hover:underline"
        >
          {t("identityProofing.overview.apiDocs")}
        </a>
      </p>
    </Card>
  );
}

// What an org sets up once for identity proofing, for all its flows: the API
// tokens and the webhook endpoint its own software uses. Admin only.
export default function Developers(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  // What shows instead of the sections to anyone but an admin, and until the
  // org is known.
  const notice = org.isPending
    ? t("common.loading")
    : t("identityProofing.developers.adminOnly");

  return (
    <>
      <TopBar
        title={t("identityProofing.developers.title")}
        subtitle={t("identityProofing.developers.subtitle")}
        actions={
          <a
            href={absoluteApiUrl(API_DOCS_PATH)}
            target="_blank"
            rel="noreferrer"
            className={buttonClassName("secondary")}
          >
            {t("identityProofing.overview.apiDocs")}
          </a>
        }
      />
      {isAdmin ? (
        <div className="flex flex-col gap-6 p-4 sm:p-8">
          <ApiTokensSection slug={slug} />
          <SigningApiNote />
          <WebhookSection slug={slug} />
        </div>
      ) : (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">{notice}</p>
      )}
    </>
  );
}
