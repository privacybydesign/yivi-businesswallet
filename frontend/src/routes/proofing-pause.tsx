import { useTranslation } from "react-i18next";
import * as React from "react";
import type { ProofingPause } from "../api/identity-proofing";
import { Card, Icon } from "../ui";

const HINT = "text-ink-soft text-[13px]";
const NOTICE_ICON_SIZE = 18;

// Why the org's proofing is stopped, above the overview's lists: a platform
// admin paused it, and only they resume it.
export function ProofingPausedNotice({
  pause,
}: Readonly<{
  pause: ProofingPause;
}>): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <Card className="flex flex-col gap-3 p-6">
      <div className="flex items-center gap-2">
        <Icon name="warning" size={NOTICE_ICON_SIZE} />
        <h2 className="font-display text-[17px] font-bold">
          {t("identityProofing.pause.pausedTitle")}
        </h2>
      </div>
      <p className={HINT}>
        {pause.platformPausedBy
          ? t("identityProofing.pause.byPlatformAdmin", {
              name: pause.platformPausedBy.name,
            })
          : t("identityProofing.pause.byPlatform")}
      </p>
      <p className={HINT}>{t("identityProofing.pause.whatStops")}</p>
    </Card>
  );
}
