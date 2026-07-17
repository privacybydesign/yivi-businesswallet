import * as React from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui";
import { toast } from "../lib/toast";

// JsonSnippet renders a labelled, scrollable, copyable JSON block. Used for the
// generated Veramo issuer config / bundle (schema editor + org issuer settings)
// that an operator copies into the issuer ops repo.
export function JsonSnippet({
  title,
  value,
}: {
  title: string;
  value: unknown;
}): React.JSX.Element {
  const { t } = useTranslation();
  const json = JSON.stringify(value, null, 2);

  function copy(): void {
    void navigator.clipboard
      .writeText(json)
      .then(() => toast.success(t("common.copied")))
      .catch(() => toast.error(t("common.copyFailed")));
  }

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="text-ink-soft text-[11.5px] font-semibold">
          {title}
        </span>
        <Button type="button" variant="ghost" size="sm" onClick={copy}>
          {t("common.copy")}
        </Button>
      </div>
      <pre className="border-line bg-surface-2 text-ink max-h-64 overflow-auto rounded-md border p-2.5 text-[11.5px] leading-relaxed">
        <code>{json}</code>
      </pre>
    </div>
  );
}
