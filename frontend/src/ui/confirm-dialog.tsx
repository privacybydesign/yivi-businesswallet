import { useTranslation } from "react-i18next";
import { Button } from "./button";
import { Modal } from "./modal";

interface ConfirmDialogProps {
  title: string;
  // The confirmation prompt shown in the dialog body (translated by the caller).
  message: string;
  // Label for the confirm action button (translated by the caller).
  confirmLabel: string;
  // Confirm button style; defaults to the destructive variant since most
  // confirmations gate a delete.
  confirmVariant?: "primary" | "danger";
  onConfirm: () => void;
  onClose: () => void;
  // Disables the confirm button while the action is in flight.
  busy?: boolean;
}

// ConfirmDialog is the in-app replacement for window.confirm: an accessible,
// theme-aware modal (focus trap + Escape handled by Modal) with a cancel and a
// confirm action, instead of the browser's native alert chrome.
export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  confirmVariant = "danger",
  onConfirm,
  onClose,
  busy = false,
}: ConfirmDialogProps): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Modal
      title={title}
      closeLabel={t("common.close")}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={confirmVariant}
            size="sm"
            onClick={onConfirm}
            disabled={busy}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      <p className="text-ink-soft text-sm">{message}</p>
    </Modal>
  );
}
