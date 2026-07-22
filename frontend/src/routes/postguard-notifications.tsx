import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  usePostguardSettingsQuery,
  useSetPostguardNotificationsMutation,
} from "../api/postguard.queries";
import type { PostguardNotificationDelivery } from "../api/postguard";
import { Button, Card, Icon } from "../ui";
import * as React from "react";

const OPTIONS = [
  {
    value: "postguard",
    titleKey: "postguard.notifications.postguardTitle",
    bodyKey: "postguard.notifications.postguardBody",
  },
  {
    value: "smtp",
    titleKey: "postguard.notifications.smtpTitle",
    bodyKey: "postguard.notifications.smtpBody",
  },
] as const satisfies readonly {
  value: PostguardNotificationDelivery;
  titleKey: string;
  bodyKey: string;
}[];

export function PostguardNotificationsCard({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = usePostguardSettingsQuery(slug);
  const save = useSetPostguardNotificationsMutation(slug);

  const stored = settings.data?.notifications ?? "postguard";
  const [choice, setChoice] = useState<PostguardNotificationDelivery | null>(
    null,
  );
  const selected = choice ?? stored;
  const dirty = selected !== stored;

  function submit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (!dirty || save.isPending) {
      return;
    }
    save.mutate(
      { notifications: selected },
      { onSuccess: () => setChoice(null) },
    );
  }

  return (
    <Card className="p-5">
      <div className="mb-2.5 flex items-center gap-2.5">
        <span className="bg-highlight text-link flex h-8.5 w-8.5 items-center justify-center rounded-lg">
          <Icon name="email" size={17} />
        </span>
        <div>
          <div className="font-display font-bold">
            {t("postguard.notifications.title")}
          </div>
          <div className="text-ink-soft text-[12.5px]">
            {t("postguard.notifications.subtitle")}
          </div>
        </div>
      </div>

      {settings.isPending ? (
        <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
      ) : !isAdmin ? (
        <p className="text-ink-soft text-[13px]">
          {t("postguard.notifications.member", {
            method:
              stored === "smtp"
                ? t("postguard.notifications.smtpTitle")
                : t("postguard.notifications.postguardTitle"),
          })}
        </p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-2">
          {OPTIONS.map((option) => (
            <label
              key={option.value}
              className="rounded-yivi border-line hover:bg-surface-2 flex cursor-pointer items-start gap-2.5 border px-3 py-2.5"
            >
              <input
                type="radio"
                name="postguard-notifications"
                className="mt-1 shrink-0"
                value={option.value}
                checked={selected === option.value}
                onChange={() => setChoice(option.value)}
              />
              <span>
                <span className="text-ink block text-[13.5px] font-semibold">
                  {t(option.titleKey)}
                </span>
                <span className="text-ink-soft block text-[12.5px]">
                  {t(option.bodyKey)}
                </span>
              </span>
            </label>
          ))}

          {selected === "smtp" && (
            <p className="text-ink-soft text-[12.5px]">
              {t("postguard.notifications.smtpHint")}
            </p>
          )}

          {save.isError && (
            <p role="alert" className="text-error text-[12px]">
              {t("postguard.notifications.error", {
                message: save.error.message,
              })}
            </p>
          )}

          <div>
            <Button
              type="submit"
              size="sm"
              loading={save.isPending}
              disabled={!dirty}
            >
              {t("common.save")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}
