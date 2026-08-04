import { useMemo } from "react";
import { useTranslation } from "react-i18next";

const MS_PER_DAY = 86_400_000;

function calendarDayDiff(now: Date, then: Date): number {
  const a = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const b = new Date(then.getFullYear(), then.getMonth(), then.getDate());
  return Math.round((a.getTime() - b.getTime()) / MS_PER_DAY);
}

// Formats a calendar date without a time — what a validity date is ("1 Jun 2026").
// Unlike useWhenFormatter this carries no Today / Yesterday labels: those read as
// past events, and a validity date is usually in the future.
export function useDateFormatter(): (iso: string) => string {
  const { i18n } = useTranslation();
  return useMemo(() => {
    const day = new Intl.DateTimeFormat(i18n.language, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
    return (iso: string): string => {
      const date = new Date(iso);
      return Number.isNaN(date.getTime()) ? iso : day.format(date);
    };
  }, [i18n.language]);
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
