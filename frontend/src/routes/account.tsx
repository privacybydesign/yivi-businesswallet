import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AVATAR_TARGET_DIMENSION } from "../api/avatar";
import {
  useRemoveMyAvatarMutation,
  useUploadMyAvatarMutation,
} from "../api/avatar.queries";
import { useMeQuery } from "../api/auth.queries";
import { prepareAvatar } from "../lib/avatar-image";
import { fullName, personInitials } from "../lib/name";
import { Avatar, Button, Card, TopBar } from "../ui";
import * as React from "react";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";

export default function Account(): React.JSX.Element | null {
  const { t } = useTranslation();
  const { data: me } = useMeQuery();
  const upload = useUploadMyAvatarMutation();
  const remove = useRemoveMyAvatarMutation();
  const [photoError, setPhotoError] = useState<string | null>(null);

  // ProtectedRoute guarantees an authenticated user before this route mounts.
  if (me == null) {
    return null;
  }

  const busy = upload.isPending || remove.isPending;

  async function handleSelect(file: File | null): Promise<void> {
    if (!file) {
      return;
    }
    setPhotoError(null);
    const prepared = await prepareAvatar(file);
    if (!prepared.ok) {
      setPhotoError(
        prepared.reason === "tooLarge"
          ? t("account.photoTooLarge")
          : t("account.photoUnreadable"),
      );
      return;
    }
    upload.mutate(prepared.photo);
  }

  return (
    <>
      <TopBar title={t("account.title")} subtitle={t("account.subtitle")} />

      <div className="flex max-w-2xl flex-col gap-6 p-8">
        <Card className="p-7">
          <h2 className="text-[16px] font-semibold">{t("account.photo")}</h2>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t("account.photoIntro")}
          </p>

          <div className="mt-5 flex items-center gap-5">
            <Avatar
              size="xl"
              initials={personInitials(me)}
              src={me.avatarUri || undefined}
              alt={t("account.photoAlt")}
              fit="cover"
            />
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center gap-2">
                <label className="rounded-yivi border-line-strong bg-surface text-ink hover:bg-surface-3 focus-within:border-ink focus-within:ring-ink/10 inline-flex h-9 cursor-pointer items-center border px-3 text-[13px] font-medium transition-colors focus-within:ring-3">
                  <input
                    type="file"
                    accept="image/*"
                    className="sr-only"
                    disabled={busy}
                    onChange={(event) => {
                      void handleSelect(event.target.files?.[0] ?? null);
                      // Clear the input so picking the same file again re-runs.
                      event.target.value = "";
                    }}
                  />
                  {me.avatarUri
                    ? t("account.photoReplace")
                    : t("account.photoChoose")}
                </label>
                {me.avatarUri !== "" && (
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => remove.mutate()}
                  >
                    {t("account.photoRemove")}
                  </Button>
                )}
              </div>
              <span className="text-muted text-[11px]">
                {t("account.photoHint", { size: AVATAR_TARGET_DIMENSION })}
              </span>
            </div>
          </div>

          {busy && (
            <p className="text-ink-soft mt-3 text-[12px]">
              {t("common.saving")}
            </p>
          )}
          {photoError && (
            <p role="alert" className="text-error mt-3 text-[12px]">
              {photoError}
            </p>
          )}
          {upload.isError && (
            <p role="alert" className="text-error mt-3 text-[12px]">
              {t("account.photoSaveError", { message: upload.error.message })}
            </p>
          )}
          {remove.isError && (
            <p role="alert" className="text-error mt-3 text-[12px]">
              {t("account.photoSaveError", { message: remove.error.message })}
            </p>
          )}
        </Card>

        <Card className="p-7">
          <h2 className="text-[16px] font-semibold">{t("account.details")}</h2>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t("account.detailsIntro")}
          </p>
          <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
            <span className={EYEBROW}>{t("common.name")}</span>
            <span className="text-ink text-[13px] font-medium">
              {fullName(me)}
            </span>
            <span className={EYEBROW}>{t("common.email")}</span>
            <span className="text-ink text-[13px] font-medium">{me.email}</span>
          </div>
        </Card>
      </div>
    </>
  );
}
