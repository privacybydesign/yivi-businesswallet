import { useMemo } from "react";
import { useTranslation } from "react-i18next";

// One decimal is enough to tell two customers' verified shares apart.
const PERCENT_FRACTION_DIGITS = 1;

// Formats a 0..1 share as a percentage in the UI language ("85.1%").
export function usePercentFormatter(): (share: number) => string {
  const { i18n } = useTranslation();
  return useMemo(() => {
    const percent = new Intl.NumberFormat(i18n.language, {
      style: "percent",
      minimumFractionDigits: PERCENT_FRACTION_DIGITS,
      maximumFractionDigits: PERCENT_FRACTION_DIGITS,
    });
    return (share: number): string => percent.format(share);
  }, [i18n.language]);
}
