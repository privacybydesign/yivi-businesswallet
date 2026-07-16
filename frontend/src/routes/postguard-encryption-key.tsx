import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useDeletePostguardEncryptionKeyMutation,
  usePostguardSettingsQuery,
  useSetPostguardEncryptionKeyMutation,
} from "../api/postguard.queries";
import { useWhenFormatter } from "../lib/format-when";
import { ApiError } from "../api/http";
import { Button, Card, Icon, Input } from "../ui";
import * as React from "react";

const BAD_REQUEST_STATUS = 400;
const GENERATED_KEY_BYTES = 24;

function errorCode(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    error.body &&
    typeof error.body === "object" &&
    "code" in error.body
  ) {
    const { code } = error.body;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function keyErrorMessage(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === BAD_REQUEST_STATUS &&
    errorCode(error) === "invalid_encryption_key"
  ) {
    return t("postguard.encryptionKey.invalid");
  }
  return t("postguard.encryptionKey.error", { message: error.message });
}

function generateKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(GENERATED_KEY_BYTES));
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function PostguardEncryptionKeyCard({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const settings = usePostguardSettingsQuery(slug);
  const save = useSetPostguardEncryptionKeyMutation(slug);
  const remove = useDeletePostguardEncryptionKeyMutation(slug);

  const [editing, setEditing] = useState(false);
  const [key, setKey] = useState("");

  const info = settings.data?.encryptionKey;
  const configured = info?.configured ?? false;

  function submit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (key.trim() === "" || save.isPending) {
      return;
    }
    save.mutate(
      { key: key.trim() },
      {
        onSuccess: () => {
          setKey("");
          setEditing(false);
        },
      },
    );
  }

  return (
    <Card className="p-5">
      <div className="mb-2.5 flex items-center gap-2.5">
        <span className="bg-highlight text-link flex h-8.5 w-8.5 items-center justify-center rounded-lg">
          <Icon name="lock" size={17} />
        </span>
        <div>
          <div className="font-display font-bold">
            {t("postguard.encryptionKey.title")}
          </div>
          <div className="text-ink-soft text-[12.5px]">
            {t("postguard.encryptionKey.subtitle")}
          </div>
        </div>
      </div>

      {settings.isPending ? (
        <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
      ) : configured && !editing ? (
        <div className="flex flex-col gap-2">
          <div className="rounded-yivi border-line bg-surface-2 flex items-center gap-2 border px-3 py-2 text-[13px]">
            <Icon name="valid" size={14} className="text-success shrink-0" />
            <span>
              {info?.updatedAt
                ? t("postguard.encryptionKey.setOn", {
                    when: formatWhen(info.updatedAt),
                  })
                : t("postguard.encryptionKey.set")}
            </span>
          </div>
          {isAdmin ? (
            <div className="flex gap-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setEditing(true)}
              >
                {t("postguard.encryptionKey.rotate")}
              </Button>
              <Button
                variant="danger"
                size="sm"
                icon="delete"
                loading={remove.isPending}
                onClick={() => remove.mutate()}
              >
                {t("postguard.encryptionKey.remove")}
              </Button>
            </div>
          ) : null}
        </div>
      ) : !isAdmin ? (
        <p className="text-ink-soft text-[13px]">
          {t("postguard.encryptionKey.notConfiguredMember")}
        </p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-2">
          <p className="text-ink-soft text-[12.5px]">
            {editing
              ? t("postguard.encryptionKey.rotateHelp")
              : t("postguard.encryptionKey.help")}
          </p>
          <Input
            value={key}
            onChange={(event) => setKey(event.target.value)}
            placeholder={t("postguard.encryptionKey.placeholder")}
            autoComplete="off"
            spellCheck={false}
            aria-label={t("postguard.encryptionKey.title")}
          />
          <button
            type="button"
            onClick={() => setKey(generateKey())}
            className="text-link hover:text-ink inline-flex w-fit items-center gap-1 text-[12.5px] font-semibold transition-colors"
          >
            <Icon name="add" size={12} />
            {t("postguard.encryptionKey.generate")}
          </button>
          {save.isError && (
            <p role="alert" className="text-error text-[12px]">
              {keyErrorMessage(save.error, t)}
            </p>
          )}
          <div className="flex gap-2">
            <Button
              type="submit"
              size="sm"
              loading={save.isPending}
              disabled={key.trim() === ""}
            >
              {t("common.save")}
            </Button>
            {editing && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEditing(false);
                  setKey("");
                  save.reset();
                }}
              >
                {t("common.cancel")}
              </Button>
            )}
          </div>
        </form>
      )}
    </Card>
  );
}
