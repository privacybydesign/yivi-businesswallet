import { useMemo } from "react";
import { useTranslation } from "react-i18next";

const MS_PER_DAY = 86_400_000;

function calendarDayDiff(now: Date, then: Date): number {
  const a = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const b = new Date(then.getFullYear(), then.getMonth(), then.getDate());
  return Math.round((a.getTime() - b.getTime()) / MS_PER_DAY);
}

// Formats an event timestamp for "when it happened": 24-hour time, with Today /
// Yesterday labels for the two most recent days and an absolute date before that.
export function useWhenFormatter(): (iso: string) => string {
  const { t, i18n } = useTranslation();
  return useMemo(() => {
    const time = new Intl.DateTimeFormat(i18n.language, {
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    const full = new Intl.DateTimeFormat(i18n.language, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
    return (iso: string): string => {
      const date = new Date(iso);
      if (Number.isNaN(date.getTime())) return iso;
      const diff = calendarDayDiff(new Date(), date);
      if (diff === 0) return t("common.todayAt", { time: time.format(date) });
      if (diff === 1) {
        return t("common.yesterdayAt", { time: time.format(date) });
      }
      return full.format(date);
    };
  }, [t, i18n.language]);
}
