import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateOrganizationMutation } from "../api/organization.queries";
import type { OrganizationDetail } from "../api/organization";
import { Button, Card } from "../ui";
import * as React from "react";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

export function OrgProfileSettings({
  org,
}: {
  org: OrganizationDetail;
}): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateOrganizationMutation(org.slug);

  const [name, setName] = useState(org.name);
  const trimmed = name.trim();
  const dirty = trimmed !== org.name;

  function handleSave(): void {
    if (trimmed === "" || !dirty) {
      return;
    }
    update.mutate({ name: trimmed });
  }

  function handleDiscard(): void {
    update.reset();
    setName(org.name);
  }

  return (
    <Card className="max-w-2xl p-7">
      <h2 className="text-[16px] font-semibold">{t("settings.orgProfile")}</h2>
      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <span className={EYEBROW}>{t("settings.name")}</span>
        <input
          className={CONTROL}
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
        <span className={EYEBROW}>{t("common.slug")}</span>
        <input
          className={`${CONTROL} font-mono`}
          defaultValue={org.slug}
          readOnly
        />
      </div>
      <div className="mt-5 flex gap-2">
        <Button
          onClick={handleSave}
          disabled={trimmed === "" || !dirty || update.isPending}
        >
          {update.isPending ? t("settings.saving") : t("settings.save")}
        </Button>
        <Button
          variant="ghost"
          onClick={handleDiscard}
          disabled={!dirty || update.isPending}
        >
          {t("settings.discard")}
        </Button>
      </div>
      {update.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {t("settings.error", { message: update.error.message })}
        </p>
      )}
    </Card>
  );
}
