import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useDeletePostguardApiKeyMutation,
  usePostguardSettingsQuery,
  useSetPostguardApiKeyMutation,
} from "../api/postguard.queries";
import { ApiError } from "../api/http";
import { Button, Card, Icon, Input } from "../ui";
import * as React from "react";

const BAD_REQUEST_STATUS = 400;

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
    errorCode(error) === "invalid_api_key"
  ) {
    return t("postguard.apiKey.invalid");
  }
  return t("postguard.apiKey.error", { message: error.message });
}

export function PostguardApiKeyCard({
  slug,
  isAdmin,
}: {
  slug: string;
  isAdmin: boolean;
}): React.JSX.Element {
  const { t } = useTranslation();
  const settings = usePostguardSettingsQuery(slug);
  const save = useSetPostguardApiKeyMutation(slug);
  const remove = useDeletePostguardApiKeyMutation(slug);

  const [editing, setEditing] = useState(false);
  const [apiKey, setApiKey] = useState("");

  const configured = settings.data?.apiKey?.configured ?? false;
  const encryptionConfigured =
    settings.data?.encryptionKey?.configured ?? false;

  function submit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (apiKey.trim() === "" || save.isPending) {
      return;
    }
    save.mutate(
      { apiKey: apiKey.trim() },
      {
        onSuccess: () => {
          setApiKey("");
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
            {t("postguard.apiKey.title")}
          </div>
          <div className="text-ink-soft text-[12.5px]">
            {t("postguard.apiKey.subtitle")}
          </div>
        </div>
      </div>

      {settings.isPending ? (
        <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
      ) : configured && !editing ? (
        <div className="flex flex-col gap-2">
          <div className="rounded-yivi border-line bg-surface-2 flex items-center gap-2 border px-3 py-2 font-mono text-[13px]">
            <Icon name="valid" size={14} className="text-success shrink-0" />
            <span>PG-••••{settings.data?.apiKey?.last4}</span>
          </div>
          {isAdmin ? (
            <div className="flex gap-2">
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setEditing(true)}
              >
                {t("postguard.apiKey.replace")}
              </Button>
              <Button
                variant="danger"
                size="sm"
                icon="delete"
                loading={remove.isPending}
                onClick={() => remove.mutate()}
              >
                {t("postguard.apiKey.remove")}
              </Button>
            </div>
          ) : (
            <p className="text-ink-soft text-[12.5px]">
              {t("postguard.apiKey.readyMember")}
            </p>
          )}
        </div>
      ) : !isAdmin ? (
        <p className="text-ink-soft text-[13px]">
          {t("postguard.apiKey.notConfiguredMember")}
        </p>
      ) : !encryptionConfigured ? (
        <p className="text-ink-soft text-[13px]">
          {t("postguard.apiKey.needsEncryptionKey")}
        </p>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-2">
          <p className="text-ink-soft text-[12.5px]">
            {t("postguard.apiKey.help")}
          </p>
          <Input
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
            placeholder="PG-…"
            autoComplete="off"
            spellCheck={false}
            aria-label={t("postguard.apiKey.title")}
          />
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
              disabled={apiKey.trim() === ""}
            >
              {t("common.save")}
            </Button>
            {editing && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setEditing(false);
                  setApiKey("");
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
