import * as React from "react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  useMeQuery,
  useRemoveMyAvatarMutation,
  useUpdateMyAvatarMutation,
} from "../api/auth.queries";
import type { AvatarFileProblem } from "../lib/avatar-file";
import { ACCEPTED_AVATAR_TYPES, avatarFileProblem } from "../lib/avatar-file";
import { fullName, personInitials } from "../lib/name";
import { Avatar, Button, Card, TopBar } from "../ui";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const FILE_BUTTON =
  "rounded-yivi border-line-strong bg-surface text-ink hover:bg-surface-3 focus-within:border-ink focus-within:ring-ink/10 inline-flex h-9 cursor-pointer items-center border px-3 text-[13px] font-medium transition-colors focus-within:ring-3";

// Explicit literal keys keep the strongly-typed t() happy (no dynamic keys).
const PROBLEM_KEYS = {
  type: "profile.photoTypeInvalid",
  size: "profile.photoTooLarge",
  empty: "profile.photoEmpty",
} as const satisfies Record<AvatarFileProblem, string>;

export default function Profile(): React.JSX.Element {
  const { t } = useTranslation();
  const me = useMeQuery();
  const upload = useUpdateMyAvatarMutation();
  const remove = useRemoveMyAvatarMutation();

  const [chosen, setChosen] = useState<File | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [problem, setProblem] = useState<AvatarFileProblem | null>(null);

  // The object URL for the local preview is revoked as soon as it is replaced or
  // the page unmounts, so a chosen file is not held after it stops being shown.
  React.useEffect(() => {
    if (!previewUrl) {
      return;
    }
    return () => URL.revokeObjectURL(previewUrl);
  }, [previewUrl]);

  function clearSelection(): void {
    setChosen(null);
    setPreviewUrl(null);
    setProblem(null);
    upload.reset();
  }

  function handleSelect(file: File | null): void {
    if (!file) {
      return;
    }
    const found = avatarFileProblem(file);
    if (found) {
      setProblem(found);
      setChosen(null);
      setPreviewUrl(null);
      return;
    }
    setProblem(null);
    setChosen(file);
    setPreviewUrl(URL.createObjectURL(file));
    upload.reset();
  }

  function handleSave(): void {
    if (!chosen) {
      return;
    }
    upload.mutate(chosen, { onSuccess: () => clearSelection() });
  }

  if (me.isPending) {
    return (
      <>
        <TopBar title={t("profile.title")} subtitle={t("profile.subtitle")} />
        <div className="p-8">
          <Card className="max-w-2xl p-6">
            <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
          </Card>
        </div>
      </>
    );
  }

  // ProtectedRoute only mounts this for a signed-in user, so `me.data` is set.
  const user = me.data!;
  const storedAvatar = user.avatarUri === "" ? undefined : user.avatarUri;
  const shownAvatar = previewUrl ?? storedAvatar;
  const busy = upload.isPending || remove.isPending;

  return (
    <>
      <TopBar title={t("profile.title")} subtitle={t("profile.subtitle")} />

      <div className="p-8">
        <Card className="max-w-2xl p-7">
          <h2 className="text-[16px] font-semibold">{t("profile.photo")}</h2>
          <p className="text-ink-soft mt-1 text-[13px]">
            {t("profile.photoIntro")}
          </p>

          <div className="mt-5 flex items-start gap-5">
            <Avatar
              src={shownAvatar}
              initials={personInitials(user)}
              size="xl"
              fit="cover"
              alt={t("profile.photoAlt")}
            />
            <div className="flex flex-col gap-1.5">
              <div className="flex flex-wrap items-center gap-2">
                <label className={FILE_BUTTON}>
                  <input
                    type="file"
                    accept={ACCEPTED_AVATAR_TYPES.join(",")}
                    className="sr-only"
                    onChange={(event) =>
                      handleSelect(event.target.files?.[0] ?? null)
                    }
                  />
                  {storedAvatar
                    ? t("profile.photoReplace")
                    : t("profile.photoChoose")}
                </label>
                {chosen && (
                  <>
                    <Button onClick={handleSave} disabled={busy}>
                      {upload.isPending
                        ? t("common.saving")
                        : t("profile.photoSave")}
                    </Button>
                    <Button
                      variant="ghost"
                      onClick={clearSelection}
                      disabled={busy}
                    >
                      {t("common.cancel")}
                    </Button>
                  </>
                )}
                {!chosen && storedAvatar && (
                  <Button
                    variant="ghost"
                    onClick={() => remove.mutate()}
                    disabled={busy}
                  >
                    {t("profile.photoRemove")}
                  </Button>
                )}
              </div>
              {chosen && (
                <span className="text-ink-soft truncate text-[12px]">
                  {chosen.name}
                </span>
              )}
              <span className="text-muted text-[11px]">
                {t("profile.photoHint")}
              </span>
            </div>
          </div>

          {problem && (
            <p role="alert" className="text-error mt-3 text-[12px]">
              {t(PROBLEM_KEYS[problem])}
            </p>
          )}
          {upload.isError && (
            <p role="alert" className="text-error mt-3 text-[13px]">
              {t("profile.photoSaveError", { message: upload.error.message })}
            </p>
          )}
          {remove.isError && (
            <p role="alert" className="text-error mt-3 text-[13px]">
              {t("profile.photoRemoveError", { message: remove.error.message })}
            </p>
          )}

          <div className="border-line mt-6 border-t pt-5">
            <h2 className="text-[16px] font-semibold">
              {t("profile.account")}
            </h2>
            <div className="mt-3.5 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-2.5 text-[13.5px]">
              <span className={EYEBROW}>{t("common.name")}</span>
              <span className="text-ink">{fullName(user)}</span>
              <span className={EYEBROW}>{t("profile.email")}</span>
              <span className="text-ink">{user.email}</span>
            </div>
            <p className="text-muted mt-3 text-[11px]">
              {t("profile.accountHint")}
            </p>
          </div>
        </Card>
      </div>
    </>
  );
}
