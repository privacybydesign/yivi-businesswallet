import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { Button, Modal } from "../ui";

const TEXTAREA =
  "border-line bg-surface text-ink mt-1.5 w-full rounded-md border px-3 py-2 text-[13px]";

interface DeclineDialogProps {
  onConfirm: (reason: string) => void;
  onClose: () => void;
  busy?: boolean;
}

// DeclineDialog is the confirmation a pending signer sees before refusing to sign
// a request — an optional reason shown to the requester, then a destructive
// confirm. Shared by the org signing page and the external signee's own page,
// since both run the same decline call, just against a different signer.
export function DeclineDialog({
  onConfirm,
  onClose,
  busy = false,
}: DeclineDialogProps): React.JSX.Element {
  const { t } = useTranslation();
  const [reason, setReason] = useState("");

  return (
    <Modal
      title={t("signing.decline.dialogTitle")}
      closeLabel={t("common.close")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            variant="danger"
            size="sm"
            onClick={() => onConfirm(reason)}
            disabled={busy}
          >
            {t("signing.decline.confirm")}
          </Button>
        </>
      }
    >
      <p className="text-ink-soft text-[13px]">
        {t("signing.decline.dialogHint")}
      </p>
      <label className="mt-3 block">
        <span className="text-ink-soft text-[12px] font-semibold">
          {t("signing.decline.reasonLabel")}
        </span>
        <textarea
          className={TEXTAREA}
          rows={3}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t("signing.decline.reasonPlaceholder")}
          aria-label={t("signing.decline.reasonLabel")}
        />
      </label>
    </Modal>
  );
}
