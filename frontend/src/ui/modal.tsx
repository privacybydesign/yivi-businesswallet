import { useEffect } from "react";
import type { ReactNode } from "react";
import { Icon } from "./icon";

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
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        onClose();
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
        role="dialog"
        aria-modal="true"
        aria-label={title}
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
