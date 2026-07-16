import { useEffect, useRef } from "react";
import type { ReactNode } from "react";
import { Icon } from "./icon";

// Selector for the elements a keyboard user can Tab between inside the dialog.
const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface ModalProps {
  title: string;
  // Accessible label for the close control (translated by the caller).
  closeLabel: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  // Widens the dialog for multi-column content such as the issue wizard.
  wide?: boolean;
}

export function Modal({
  title,
  closeLabel,
  onClose,
  children,
  footer,
  wide = false,
}: ModalProps): React.JSX.Element {
  const dialogRef = useRef<HTMLDivElement>(null);

  // Move focus into the dialog on open and restore it to the trigger on close.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    const first = dialog?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? dialog)?.focus();
    return () => previouslyFocused?.focus?.();
  }, []);

  // Escape closes; Tab is trapped inside the dialog.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      const dialog = dialogRef.current;
      if (event.key !== "Tab" || !dialog) {
        return;
      }
      const items = dialog.querySelectorAll<HTMLElement>(FOCUSABLE);
      if (items.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && (active === first || active === dialog)) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-6"
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
        className={[
          "rounded-yivi bg-surface shadow-card my-8 flex w-full flex-col border",
          "border-line",
          wide ? "max-w-2xl" : "max-w-lg",
        ].join(" ")}
      >
        <div className="border-line flex items-center justify-between gap-4 border-b px-5 py-3.5">
          <h2 className="text-ink text-[16px] font-bold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={closeLabel}
            className="text-ink-soft hover:text-ink transition-colors"
          >
            <Icon name="close" size={18} />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer && (
          <div className="border-line flex items-center justify-end gap-2 border-t px-5 py-3.5">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}
