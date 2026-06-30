import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { getToasts, subscribeToasts, toast } from "../lib/toast";
import type { ToastTone } from "../lib/toast";
import { Icon } from "./icon";
import type { IconName } from "./icon";

const TONE_ICON: Record<ToastTone, IconName> = {
  success: "valid",
  error: "warning",
  info: "info",
};

const TONE_COLOR: Record<ToastTone, string> = {
  success: "text-success",
  error: "text-error",
  info: "text-link",
};

// App-global like the sidebar, so it owns its own copy (the close label); the
// toast messages themselves arrive already translated from the call site.
export function Toaster(): React.JSX.Element {
  const { t } = useTranslation();
  const toasts = useSyncExternalStore(subscribeToasts, getToasts, getToasts);

  return (
    <div
      aria-live="polite"
      className="pointer-events-none fixed right-4 bottom-4 z-50 flex w-[min(360px,calc(100vw-2rem))] flex-col gap-2"
    >
      {toasts.map((item) => (
        <div
          key={item.id}
          role={item.tone === "error" ? "alert" : "status"}
          className="bg-surface border-line rounded-yivi shadow-card pointer-events-auto flex items-start gap-2.5 border px-3.5 py-3"
        >
          <span className={["mt-px shrink-0", TONE_COLOR[item.tone]].join(" ")}>
            <Icon name={TONE_ICON[item.tone]} size={16} />
          </span>
          <p className="text-ink flex-1 text-[13px] leading-snug">
            {item.message}
          </p>
          <button
            type="button"
            onClick={() => toast.dismiss(item.id)}
            aria-label={t("toasts.dismiss")}
            className="text-muted hover:text-ink shrink-0 cursor-pointer transition-colors"
          >
            <Icon name="close" size={14} />
          </button>
        </div>
      ))}
    </div>
  );
}
