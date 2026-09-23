import * as React from "react";
import { useTranslation } from "react-i18next";
import { useMintOwnVogLinkMutation } from "../api/organization.queries";
import type { OwnVogState } from "../api/organization";
import { useDateFormatter } from "../lib/format-when";
import { Button, Card, Icon } from "../ui";

// VogBanner is the member's own prompt to submit a VOG: it mints a fresh VOG
// link for their membership and opens it, the same mechanism the request and
// reminder e-mails use (mirroring IdentityBanner).
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
  const mint = useMintOwnVogLinkMutation(slug);
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
        loading={mint.isPending}
        onClick={() =>
          mint.mutate(undefined, {
            // The link is a full URL from the backend, and the page it opens is
            // outside the app shell, so it is a document navigation rather than
            // a router push.
            onSuccess: (url) => window.location.assign(url),
          })
        }
      >
        {t("vog.banner.action")}
      </Button>
      {mint.isError && (
        <p role="alert" className="text-error text-[12.5px]">
          {t("vog.banner.error", { message: mint.error.message })}
        </p>
      )}
    </Card>
  );
}
