import * as React from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { OwnVogState } from "../api/organization";
import { useDateFormatter } from "../lib/format-when";
import { Button, Card, Icon } from "../ui";

// VogBanner is the member's own prompt to submit a VOG: it points at the VOG
// screening page (an ordinary in-app page, unlike re-identification's
// bearer-token link - a member being screened already has an account).
export function VogBanner({
  slug,
  orgName,
  vog,
}: {
  slug: string;
  orgName: string;
  vog: OwnVogState;
}): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const formatDate = useDateFormatter();

  const message = (): string => {
    const date = vog.validUntil ? formatDate(vog.validUntil) : "";
    switch (vog.status) {
      case "requested":
        return t("vog.banner.requested", { org: orgName });
      case "expiring":
        return t("vog.banner.expiring", { org: orgName, date });
      case "expired":
        return t("vog.banner.expired", { org: orgName, date });
      case "rejected":
        return t("vog.banner.rejected", { org: orgName });
      case "recheck_required":
        return t("vog.banner.recheckRequired", { org: orgName });
      default:
        return t("vog.banner.none", { org: orgName });
    }
  };

  const urgent = vog.status === "expired" || vog.status === "rejected";

  return (
    <Card
      className={`flex flex-col gap-3 p-4 sm:flex-row sm:items-center ${
        urgent ? "border-error" : ""
      }`}
    >
      <span
        className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full ${
          urgent ? "bg-error-bg text-error" : "bg-warning-bg text-warning-fg"
        }`}
      >
        <Icon name={urgent ? "warning" : "time"} size={15} />
      </span>
      <p className="text-ink flex-1 text-[13.5px]">{message()}</p>
      <Button
        variant={urgent ? "primary" : "secondary"}
        onClick={() => void navigate(`/${slug}/vog`)}
      >
        {t("vog.banner.action")}
      </Button>
    </Card>
  );
}
