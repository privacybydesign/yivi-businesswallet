import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateOrganizationMutation } from "../api/organization.queries";
import type { OrganizationDetail } from "../api/organization";
import { Button, Card, Input } from "../ui";
import { DepartmentSettings } from "./department-settings";
import * as React from "react";

const TERM = "text-ink-soft";
const MONO = "font-mono text-[13px]";
const DETAILS =
  "grid grid-cols-1 gap-x-4 gap-y-1 px-5 py-5 text-[13.5px] wrap-anywhere sm:grid-cols-[160px_minmax(0,1fr)] sm:items-center sm:gap-y-3";
const CARD_HEADER = "border-line border-b px-5 py-4";
const CARD_TITLE = "font-display text-[17px] font-bold";
const NAME_INPUT_ID = "org-name";
const EDIT_BUTTON_ID = "org-edit-name";

// The organisation's lifecycle statuses the backend sets; anything else is
// shown as it comes.
const STATUS_LABEL_KEYS = {
  active: "settings.organisation.statuses.active",
  suspended: "settings.organisation.statuses.suspended",
  revoked: "settings.organisation.statuses.revoked",
} as const;

function isKnownStatus(
  status: string,
): status is keyof typeof STATUS_LABEL_KEYS {
  return Object.hasOwn(STATUS_LABEL_KEYS, status);
}

export function OrgProfileSettings({
  org,
}: Readonly<{
  org: OrganizationDetail;
}>): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <div className="flex max-w-2xl flex-col gap-6">
      <OrgNameCard org={org} />
      <BusinessWalletCard org={org} />
      <DepartmentSettings slug={org.slug} />
      <p className="text-muted text-[12.5px]">
        {t("settings.organisation.internalId")}{" "}
        <span className="font-mono">{org.id}</span>
      </p>
    </div>
  );
}

function OrgNameCard({
  org,
}: Readonly<{ org: OrganizationDetail }>): React.JSX.Element {
  const { t } = useTranslation();
  const update = useUpdateOrganizationMutation(org.slug);
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(org.name);
  const wasEditing = useRef(false);
  const trimmed = name.trim();
  const dirty = trimmed !== org.name;

  // Keyboard focus follows the change: into the field when editing starts,
  // back to the button when the field goes away.
  useEffect(() => {
    if (editing) document.getElementById(NAME_INPUT_ID)?.focus();
    else if (wasEditing.current)
      document.getElementById(EDIT_BUTTON_ID)?.focus();

    wasEditing.current = editing;
  }, [editing]);

  function startEditing(): void {
    setName(org.name);
    setEditing(true);
  }

  function stopEditing(): void {
    update.reset();
    setEditing(false);
  }

  function handleSave(event: React.FormEvent): void {
    event.preventDefault();
    if (trimmed === "" || !dirty) {
      return;
    }
    update.mutate({ name: trimmed }, { onSuccess: () => setEditing(false) });
  }

  return (
    <Card>
      <form onSubmit={handleSave} noValidate>
        <div
          className={`${CARD_HEADER} flex flex-wrap items-center justify-between gap-3`}
        >
          <h2 className={CARD_TITLE}>{t("settings.organisation.title")}</h2>
          {!editing && (
            <Button
              id={EDIT_BUTTON_ID}
              type="button"
              variant="secondary"
              onClick={startEditing}
            >
              {t("settings.organisation.editName")}
            </Button>
          )}
        </div>
        <dl className={DETAILS}>
          <dt className={TERM}>
            {editing ? (
              <label htmlFor={NAME_INPUT_ID}>
                {t("settings.organisation.name")}
              </label>
            ) : (
              t("settings.organisation.name")
            )}
          </dt>
          <dd className="m-0">
            {editing ? (
              <Input
                id={NAME_INPUT_ID}
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            ) : (
              org.name
            )}
          </dd>
          <dt className={TERM}>{t("settings.organisation.shortName")}</dt>
          <dd className={`${MONO} m-0`}>{org.slug}</dd>
        </dl>
        {update.isError && (
          <p role="alert" className="text-error px-5 pb-4 text-[13px]">
            {t("common.saveError", { message: update.error.message })}
          </p>
        )}
        {editing && (
          <div className="border-line flex justify-end gap-2 border-t px-5 py-4">
            <Button
              type="button"
              variant="secondary"
              disabled={update.isPending}
              onClick={stopEditing}
            >
              {t("settings.discard")}
            </Button>
            <Button
              type="submit"
              loading={update.isPending}
              disabled={trimmed === "" || !dirty}
            >
              {t("common.save")}
            </Button>
          </div>
        )}
      </form>
    </Card>
  );
}

function BusinessWalletCard({
  org,
}: Readonly<{
  org: OrganizationDetail;
}>): React.JSX.Element {
  const { t } = useTranslation();

  return (
    <Card>
      <div className={CARD_HEADER}>
        <h2 className={CARD_TITLE}>
          {t("settings.organisation.wallet.title")}
        </h2>
        <p className="text-muted mt-1 text-[12px]">
          {t("settings.organisation.wallet.hint")}
        </p>
      </div>
      <dl className={DETAILS}>
        <dt className={TERM}>{t("settings.organisation.wallet.kvkNumber")}</dt>
        <dd className={`${MONO} m-0`}>{org.kvkNumber}</dd>
        <dt className={TERM}>{t("settings.organisation.wallet.euid")}</dt>
        <dd className={`${MONO} m-0`}>{org.euid}</dd>
        <dt className={TERM}>{t("settings.organisation.wallet.address")}</dt>
        <dd className={`${MONO} m-0`}>
          {org.digitalAddress || t("settings.organisation.wallet.noAddress")}
        </dd>
        <dt className={TERM}>{t("settings.organisation.wallet.status")}</dt>
        <dd className="m-0">
          {isKnownStatus(org.status)
            ? t(STATUS_LABEL_KEYS[org.status])
            : org.status}
        </dd>
      </dl>
    </Card>
  );
}
