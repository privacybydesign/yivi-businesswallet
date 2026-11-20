import { useTranslation } from "react-i18next";
import { Input } from "../ui";
import * as React from "react";

// The company name a registrant states, asked on both registration forms.
export function CompanyNameField({
  value,
  onChange,
}: Readonly<{
  value: string;
  onChange: (value: string) => void;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-ink text-[13px] font-semibold">
        {t("register.companyName")}
      </span>
      <Input
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={t("register.companyNamePlaceholder")}
      />
      <span className="text-muted text-[12px]">
        {t("register.companyNameHint")}
      </span>
    </label>
  );
}
