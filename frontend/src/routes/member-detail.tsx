import { useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
} from "../api/organization.queries";
import { accessMessage } from "../lib/access-message";
import { fullName, personInitials } from "../lib/name";
import { Avatar, Button, Card, Tag, TopBar } from "../ui";
import * as React from "react";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";

function DetailRow({
  label,
  value,
  capitalize,
}: {
  label: string;
  value: string;
  capitalize?: boolean;
}): React.JSX.Element {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className={EYEBROW}>{label}</span>
      <span
        className={[
          "text-ink text-[13px] font-medium",
          capitalize ? "capitalize" : "",
        ].join(" ")}
      >
        {value}
      </span>
    </div>
  );
}

export default function MemberDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug, userId } = useParams();
  // Both are guaranteed by the ":orgSlug/members/:userId" route.
  const slug = orgSlug!;
  const id = userId!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const members = useOrganizationMembersQuery(slug, isAdmin);
  const member = members.data?.find((m) => m.userId === id);

  const shell = (body: React.ReactNode): React.JSX.Element => (
    <>
      <TopBar title={t("memberDetail.title")} />
      <div className="p-8">{body}</div>
    </>
  );
  const message = (text: string, isError = false): React.JSX.Element => (
    <Card className="p-6">
      <p className={`text-[14px] ${isError ? "text-error" : "text-ink-soft"}`}>
        {text}
      </p>
    </Card>
  );

  if (org.isError) {
    return shell(message(accessMessage(org.error, t), true));
  }
  if (org.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!isAdmin) {
    return shell(message(t("members.adminOnly")));
  }
  if (members.isError) {
    return shell(message(accessMessage(members.error, t), true));
  }
  if (members.isPending) {
    return shell(message(t("common.loading")));
  }
  if (!member) {
    return shell(message(t("memberDetail.notFound")));
  }

  const subtitleParts = [member.jobTitle, member.departmentName].filter(
    Boolean,
  );
  const subtitle =
    subtitleParts.length > 0 ? subtitleParts.join(" · ") : undefined;

  return (
    <>
      <TopBar
        title={fullName(member)}
        subtitle={subtitle}
        actions={
          <>
            <Button
              variant="secondary"
              onClick={() => void navigate(`/${slug}/members/${id}/edit`)}
            >
              {t("common.edit")}
            </Button>
            <Button icon="add">{t("memberDetail.issue")}</Button>
          </>
        }
      />

      <div className="grid grid-cols-1 gap-5 p-8 lg:grid-cols-[1fr_320px]">
        <div className="flex flex-col gap-4">
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("memberDetail.attestations")}
            </h2>
            <p className="text-ink-soft mt-2 text-[14px]">
              {t("memberDetail.attestationsPlaceholder")}
            </p>
          </Card>
          <Card className="p-6">
            <h2 className="text-[16px] font-semibold">
              {t("memberDetail.timeline")}
            </h2>
            <p className="text-ink-soft mt-2 text-[14px]">
              {t("memberDetail.timelinePlaceholder")}
            </p>
          </Card>
        </div>

        <Card className="h-fit p-0">
          <div className="border-line flex flex-col items-center gap-3 border-b p-6">
            <Avatar initials={personInitials(member)} size="lg" />
            <div className="text-center">
              <div className="font-display text-[18px] font-bold">
                {fullName(member)}
              </div>
              <div className="text-ink-soft text-[12.5px]">{member.email}</div>
            </div>
            <Tag tone="green" dot>
              {t("memberDetail.active")}
            </Tag>
          </div>
          <div className="flex flex-col gap-2.5 p-5">
            <DetailRow
              label={t("common.role")}
              value={member.role}
              capitalize
            />
            <DetailRow
              label={t("common.jobTitle")}
              value={member.jobTitle ?? "—"}
            />
            <DetailRow
              label={t("common.department")}
              value={member.departmentName ?? "—"}
            />
          </div>
          <div className="border-line flex flex-col gap-2 border-t p-4">
            <Button variant="secondary" icon="email" className="w-full">
              {t("memberDetail.sendMessage")}
            </Button>
            <Button variant="danger" icon="logout" className="w-full">
              {t("memberDetail.offboard")}
            </Button>
          </div>
        </Card>
      </div>
    </>
  );
}
