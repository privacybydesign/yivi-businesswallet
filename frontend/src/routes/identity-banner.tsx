import * as React from "react";
import { useTranslation } from "react-i18next";
import { useMintOwnReidentifyLinkMutation } from "../api/organization.queries";
import type { OwnIdentityState } from "../api/organization";
import { useDateFormatter } from "../lib/format-when";
import { Button, Card, Icon } from "../ui";

// IdentityBanner is the member's own prompt to re-identify: it mints a fresh
// link for their membership and opens it, which is the same mechanism the
// reminder e-mail and an admin's request use.
export function IdentityBanner({
  slug,
  orgName,
  identity,
}: {
  slug: string;
  orgName: string;
  identity: OwnIdentityState;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatDate = useDateFormatter();
  const mint = useMintOwnReidentifyLinkMutation(slug);

  const message = (): string => {
    const date = identity.dueAt ? formatDate(identity.dueAt) : "";
    switch (identity.status) {
      case "requested":
        return t("reidentify.banner.requested", { org: orgName });
      case "due_soon":
        return t("reidentify.banner.dueSoon", { org: orgName, date });
      default:
        return identity.dueAt
          ? t("reidentify.banner.overdue", { org: orgName, date })
          : t("reidentify.banner.overdueNoDate", { org: orgName });
    }
  };

  const overdue = identity.status === "overdue";

  return (
    <Card
      className={`flex flex-col gap-3 p-4 sm:flex-row sm:items-center ${
        overdue ? "border-error" : ""
      }`}
    >
      <span
        className={`inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full ${
          overdue ? "bg-error-bg text-error" : "bg-warning-bg text-warning-fg"
        }`}
      >
        <Icon name={overdue ? "warning" : "time"} size={15} />
      </span>
      <p className="text-ink flex-1 text-[13.5px]">{message()}</p>
      <Button
        variant={overdue ? "primary" : "secondary"}
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
        {t("reidentify.banner.action")}
      </Button>
      {mint.isError && (
        <p role="alert" className="text-error text-[12.5px]">
          {t("reidentify.banner.error", { message: mint.error.message })}
        </p>
      )}
    </Card>
  );
}
