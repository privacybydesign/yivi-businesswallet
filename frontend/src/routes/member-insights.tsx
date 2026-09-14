import * as React from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useMemberInsightsQuery } from "../api/organization.queries";
import {
  identityStatusLabel,
  identityStatusTone,
} from "../lib/identity-status";
import {
  IDENTITY_STATUS_ORDER,
  notIdentifiedCount,
  SCREENING_STATUS_ORDER,
  screeningApplies,
  screeningAttentionCount,
  statusSegments,
} from "../lib/member-insights";
import {
  screeningStatusLabel,
  screeningStatusTone,
} from "../lib/screening-status";
import { Button, Card, Stat, Tag } from "../ui";

type Tone = "default" | "green" | "amber" | "red" | "blue";

// The bar segment colours, matching Tag's tones so the bar and its legend agree.
const SEGMENT_CLASSES: Record<Tone, string> = {
  default: "bg-surface-3",
  green: "bg-success",
  amber: "bg-warning-fg",
  red: "bg-error",
  blue: "bg-link",
};

interface StatusBarProps {
  title: string;
  counts: Record<string, number>;
  order: readonly string[];
  tone: (status: string) => Tone;
  label: (status: string, t: TFunction) => string;
}

// StatusBar is a stacked proportional bar of one status dimension with a
// legend - drawn from divs, the way ui/stepper.tsx draws its meter, rather
// than pulling in a charting library for one bar.
function StatusBar({
  title,
  counts,
  order,
  tone,
  label,
}: StatusBarProps): React.JSX.Element {
  const { t } = useTranslation();
  const segments = statusSegments(counts, order);
  return (
    <div>
      <h3 className="text-muted font-mono text-[11px] font-medium tracking-[0.08em] uppercase">
        {title}
      </h3>
      <div
        role="img"
        aria-label={segments
          .map((s) => `${label(s.status, t)}: ${s.count}`)
          .join(", ")}
        className="bg-surface-3 mt-2 flex h-2.5 w-full overflow-hidden rounded-full"
      >
        {segments.map((s) => (
          <span
            key={s.status}
            className={SEGMENT_CLASSES[tone(s.status)]}
            style={{ width: `${s.share}%` }}
          />
        ))}
      </div>
      <ul className="mt-2 flex flex-wrap gap-2">
        {segments.map((s) => (
          <li key={s.status}>
            <Tag tone={tone(s.status)} dot>
              {label(s.status, t)} · {s.count}
            </Tag>
          </li>
        ))}
      </ul>
    </div>
  );
}

interface Props {
  slug: string;
}

// MemberInsights is the org dashboard's admin overview: how many members have
// not identified and how many have no valid VOG, with the breakdown per status
// behind each number. Counts come from the backend (member-insights), which
// derives them the same way the member list derives its per-row badges.
export function MemberInsights({ slug }: Props): React.JSX.Element | null {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const insights = useMemberInsightsQuery(slug, true);

  if (insights.isError) {
    return (
      <Card className="p-6">
        <h2 className="text-[16px] font-semibold">
          {t("dashboard.insights.title")}
        </h2>
        <p role="alert" className="text-error mt-2 text-[13.5px]">
          {t("dashboard.insights.error", { message: insights.error.message })}
        </p>
      </Card>
    );
  }
  if (insights.isPending) {
    return (
      <Card className="p-6">
        <h2 className="text-[16px] font-semibold">
          {t("dashboard.insights.title")}
        </h2>
        <p className="text-ink-soft mt-2 text-[13.5px]">
          {t("common.loading")}
        </p>
      </Card>
    );
  }

  const data = insights.data;
  const notIdentified = notIdentifiedCount(data.identity);
  const vogAttention = screeningAttentionCount(data.screening);
  const vogApplies = screeningApplies(data.screening);

  return (
    <Card className="flex flex-col gap-5 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-[16px] font-semibold">
          {t("dashboard.insights.title")}
        </h2>
        <Button
          variant="secondary"
          icon="arrow_front"
          onClick={() => void navigate(`/${slug}/members`)}
        >
          {t("dashboard.viewMembers")}
        </Button>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat
          label={t("dashboard.insights.members")}
          value={data.members}
          icon="personal"
        />
        <Stat
          label={t("dashboard.insights.notIdentified")}
          value={notIdentified}
          hint={t("dashboard.insights.notIdentifiedHint")}
          icon={notIdentified > 0 ? "warning" : "valid"}
        />
        <Stat
          label={t("dashboard.insights.vogAttention")}
          value={vogApplies ? vogAttention : "—"}
          hint={
            vogApplies
              ? t("dashboard.insights.vogAttentionHint")
              : t("dashboard.insights.vogNotRequired")
          }
          icon={vogApplies && vogAttention > 0 ? "warning" : "valid"}
        />
      </div>

      {data.members === 0 ? (
        <p className="text-ink-soft text-[13.5px]">
          {t("dashboard.insights.empty")}
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          <StatusBar
            title={t("dashboard.insights.identityBar")}
            counts={data.identity}
            order={IDENTITY_STATUS_ORDER}
            tone={identityStatusTone}
            label={identityStatusLabel}
          />
          {vogApplies && (
            <StatusBar
              title={t("dashboard.insights.screeningBar")}
              counts={data.screening}
              order={SCREENING_STATUS_ORDER}
              tone={screeningStatusTone}
              label={screeningStatusLabel}
            />
          )}
        </div>
      )}
    </Card>
  );
}
