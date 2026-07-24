import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useWscaStatusQuery } from "../api/wsca.queries";
import { Card, Tag } from "../ui";

// Matches the primary Button (md) so the deep-link reads as the page's main
// call-to-action while staying a real navigation anchor.
const ACTION_CLASSES =
  "rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-9 shrink-0 items-center justify-center px-3.5 text-[13.5px] font-semibold whitespace-nowrap transition-colors duration-150";

// WscaActivationNotice guides an org admin to activate the organization's WSCA
// holder wallet before it can receive credentials. The WSCA status endpoint is
// org-admin only, so this is gated on isAdmin (a non-admin can neither read the
// status nor activate). It renders nothing unless WSCA is configured on the
// deployment and this org has not activated yet — so unconfigured deployments
// and already-activated orgs see no change.
export function WscaActivationNotice({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  const status = useWscaStatusQuery(slug, isAdmin);

  if (!isAdmin || !status.data?.configured || status.data.activated) {
    return null;
  }

  return (
    <Card className="flex flex-col gap-3 p-5 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <Tag tone="amber">{t("attestations.walletActivation.badge")}</Tag>
          <h2 className="text-ink text-[15px] font-semibold">
            {t("attestations.walletActivation.title")}
          </h2>
        </div>
        <p className="text-ink-soft mt-1.5 text-[13px]">
          {t("attestations.walletActivation.body")}
        </p>
      </div>
      <Link to={`/${slug}/settings?tab=wallets`} className={ACTION_CLASSES}>
        {t("attestations.walletActivation.action")}
      </Link>
    </Card>
  );
}
