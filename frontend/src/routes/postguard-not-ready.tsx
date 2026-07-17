import { useTranslation } from "react-i18next";
import { Card, Icon } from "../ui";
import * as React from "react";

// Indicator shown when PostGuard cannot be used yet: the org has not been
// configured, or the feature is off for the whole deployment.
export function PostguardNotReady({
  reason,
  isAdmin,
}: {
  reason: "unconfigured" | "deploymentOff";
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const deploymentOff = reason === "deploymentOff";

  const title = deploymentOff
    ? t("postguard.notReady.deploymentTitle")
    : t("postguard.notReady.title");
  const body = deploymentOff
    ? t("postguard.notReady.deploymentBody")
    : isAdmin
      ? t("postguard.notReady.adminBody")
      : t("postguard.notReady.memberBody");

  return (
    <Card className="p-6">
      <div className="flex items-start gap-3">
        <span className="bg-highlight text-link flex h-9 w-9 shrink-0 items-center justify-center rounded-lg">
          <Icon name={deploymentOff ? "warning" : "lock"} size={18} />
        </span>
        <div>
          <div className="font-display font-bold">{title}</div>
          <p className="text-ink-soft mt-1 text-[13px] leading-relaxed">
            {body}
          </p>
        </div>
      </div>
    </Card>
  );
}
