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
  // Why the action failed (translated by the caller); the dialog stays open so
  // the user sees it and can retry.
  error?: string;
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
  error,
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
      {error !== undefined && (
        <p role="alert" className="text-error mt-2 text-[12.5px]">
          {error}
        </p>
      )}
    </Modal>
  );
}
