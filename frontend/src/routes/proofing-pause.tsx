import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useSetProofingPauseMutation } from "../api/identity-proofing.queries";
import type { ProofingPause } from "../api/identity-proofing";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Icon } from "../ui";

const HINT = "text-ink-soft text-[13px]";
const ERROR = "text-error text-[12.5px]";
const NOTICE_ICON_SIZE = 18;

// Why the org's proofing is stopped, in place of the overview: a platform
// admin's pause (only they lift it) or the org admin's own switch, which an
// admin can turn back on here.
export function ProofingPausedNotice({
  slug,
  pause,
  isAdmin,
}: {
  slug: string;
  pause: ProofingPause;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const resume = useSetProofingPauseMutation(slug);
  const byPlatform = pause.platformPausedAt !== undefined;

  return (
    <Card className="flex flex-col gap-3 p-6">
      <div className="flex items-center gap-2">
        <Icon name="warning" size={NOTICE_ICON_SIZE} />
        <h2 className="font-display text-[17px] font-bold">
          {t("identityProofing.pause.pausedTitle")}
        </h2>
      </div>
      <p className={HINT}>
        {byPlatform
          ? t("identityProofing.pause.byPlatform")
          : t("identityProofing.pause.byOrganization")}
      </p>
      <p className={HINT}>{t("identityProofing.pause.whatStops")}</p>
      {isAdmin && !byPlatform && (
        <div>
          <Button
            icon="valid"
            loading={resume.isPending}
            onClick={() => resume.mutate(false)}
          >
            {t("identityProofing.pause.turnOn")}
          </Button>
        </div>
      )}
      {resume.isError && (
        <p className={ERROR}>{proofingErrorMessage(resume.error, t)}</p>
      )}
    </Card>
  );
}

// The org admin's switch to stop the org's identity proofing, confirmed first.
export function ProofingPauseCard({
  slug,
}: {
  slug: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const pause = useSetProofingPauseMutation(slug);
  const [confirming, setConfirming] = useState(false);

  return (
    <Card className="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
      <div className="min-w-0 flex-1">
        <h2 className="font-display text-[15px] font-bold">
          {t("identityProofing.pause.switchTitle")}
        </h2>
        <p className={`${HINT} mt-0.5`}>
          {t("identityProofing.pause.switchHint")}
        </p>
        {pause.isError && (
          <p className={`${ERROR} mt-1`}>
            {proofingErrorMessage(pause.error, t)}
          </p>
        )}
      </div>
      <Button
        variant="secondary"
        icon="warning"
        className="shrink-0"
        onClick={() => setConfirming(true)}
      >
        {t("identityProofing.pause.turnOff")}
      </Button>
      {confirming && (
        <ConfirmDialog
          title={t("identityProofing.pause.confirmTitle")}
          message={t("identityProofing.pause.whatStops")}
          confirmLabel={t("identityProofing.pause.turnOff")}
          busy={pause.isPending}
          onConfirm={() =>
            pause.mutate(true, { onSuccess: () => setConfirming(false) })
          }
          onClose={() => setConfirming(false)}
        />
      )}
    </Card>
  );
}
