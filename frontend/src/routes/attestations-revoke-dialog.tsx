import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { Button, Modal } from "../ui";

const TEXTAREA =
  "border-line bg-surface text-ink mt-1.5 w-full rounded-md border px-3 py-2 text-[13px]";

// Mirrors MaxRevocationReasonLength in backend/internal/attestation/attestation.go.
export const MAX_REVOCATION_REASON_LENGTH = 500;

interface RevokeDialogProps {
  onConfirm: (reason: string) => void;
  onClose: () => void;
  busy?: boolean;
  error?: string;
}

// RevokeDialog asks why a credential is revoked before revoking it: the holder
// is told that reason (WE BUILD CS-10 ISS-RV-09), so it is required.
export function RevokeDialog({
  onConfirm,
  onClose,
  busy = false,
  error,
}: Readonly<RevokeDialogProps>): React.JSX.Element {
  const { t } = useTranslation();
  const [reason, setReason] = useState("");
  const trimmed = reason.trim();

  return (
    <Modal
      title={t("attestations.issued.revokeDialog.title")}
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
            onClick={() => onConfirm(trimmed)}
            disabled={busy || trimmed === ""}
          >
            {t("attestations.issued.revokeDialog.confirm")}
          </Button>
        </>
      }
    >
      <p className="text-ink-soft text-[13px]">
        {t("attestations.issued.revokeDialog.hint")}
      </p>
      <label className="mt-3 block">
        <span className="text-ink-soft text-[12px] font-semibold">
          {t("attestations.issued.revokeDialog.reasonLabel")}
        </span>
        <textarea
          className={TEXTAREA}
          rows={3}
          maxLength={MAX_REVOCATION_REASON_LENGTH}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder={t("attestations.issued.revokeDialog.reasonPlaceholder")}
          aria-label={t("attestations.issued.revokeDialog.reasonLabel")}
        />
      </label>
      {error !== undefined && (
        <p role="alert" className="text-danger mt-2 text-[12.5px]">
          {error}
        </p>
      )}
    </Modal>
  );
}
