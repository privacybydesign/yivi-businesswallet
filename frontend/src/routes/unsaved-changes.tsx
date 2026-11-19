import { useEffect } from "react";
import { useBlocker } from "react-router";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "../ui";
import * as React from "react";

// Guards a form's unsaved edits: leaving the page inside the app asks first,
// and closing or reloading the tab gets the browser's own warning. Render it
// inside the form with `dirty` false while a save is under way.
export function UnsavedChanges({
  dirty,
}: Readonly<{ dirty: boolean }>): React.JSX.Element | null {
  const { t } = useTranslation();
  const blocker = useBlocker(dirty);

  useEffect(() => {
    if (!dirty) {
      return;
    }
    function warn(event: BeforeUnloadEvent): void {
      event.preventDefault();
    }
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  if (blocker.state !== "blocked") {
    return null;
  }
  return (
    <ConfirmDialog
      title={t("settings.unsaved.title")}
      message={t("settings.unsaved.message")}
      confirmLabel={t("settings.unsaved.leave")}
      onClose={() => blocker.reset()}
      onConfirm={() => blocker.proceed()}
    />
  );
}
