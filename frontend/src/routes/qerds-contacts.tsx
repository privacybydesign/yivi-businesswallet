import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useCreateQerdsContactMutation,
  useDeleteQerdsContactMutation,
  useQerdsContactsQuery,
} from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import { Button, Card, Input, TopBar } from "../ui";
import * as React from "react";

const CONFLICT_STATUS = 409;

function errorCode(error: ApiError): string | null {
  const body = error.body;
  if (typeof body === "object" && body !== null && "code" in body) {
    const code = (body as { code?: unknown }).code;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function contactError(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    errorCode(error) === "address_taken"
  ) {
    return t("qerds.contacts.taken");
  }
  return t("qerds.contacts.error", { message: error.message });
}

export default function QerdsContacts(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const contacts = useQerdsContactsQuery(slug, !org.isError);
  const create = useCreateQerdsContactMutation(slug);
  const remove = useDeleteQerdsContactMutation(slug);

  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [legalName, setLegalName] = useState("");
  const [kvkNumber, setKvkNumber] = useState("");
  const [euid, setEuid] = useState("");

  function handleCreate(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    const trimmedName = name.trim();
    const trimmedAddress = address.trim();
    if (trimmedName === "" || trimmedAddress === "") {
      return;
    }
    create.mutate(
      {
        name: trimmedName,
        address: trimmedAddress,
        legalName: legalName.trim() || undefined,
        kvkNumber: kvkNumber.trim() || undefined,
        euid: euid.trim() || undefined,
      },
      {
        onSuccess: () => {
          setName("");
          setAddress("");
          setLegalName("");
          setKvkNumber("");
          setEuid("");
        },
      },
    );
  }

  const body = (): React.JSX.Element => {
    if (org.isError) {
      return (
        <Card className="p-6">
          <p className="text-error text-[14px]">
            {accessMessage(org.error, t)}
          </p>
        </Card>
      );
    }
    return (
      <Card className="max-w-2xl p-7">
        <h2 className="text-[16px] font-semibold">
          {t("qerds.contacts.heading")}
        </h2>
        <p className="text-ink-soft mt-1 text-[13px]">
          {t("qerds.contacts.description")}
        </p>

        <form onSubmit={handleCreate} className="mt-4 flex flex-col gap-2">
          <div className="flex gap-2">
            <div className="w-[34%]">
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t("qerds.contacts.namePlaceholder")}
                aria-label={t("qerds.contacts.namePlaceholder")}
              />
            </div>
            <div className="flex-1">
              <Input
                value={address}
                onChange={(event) => setAddress(event.target.value)}
                placeholder={t("qerds.contacts.addressPlaceholder")}
                aria-label={t("qerds.contacts.addressPlaceholder")}
              />
            </div>
          </div>
          <div className="flex gap-2">
            <div className="flex-1">
              <Input
                value={legalName}
                onChange={(event) => setLegalName(event.target.value)}
                placeholder={t("qerds.contacts.legalNamePlaceholder")}
                aria-label={t("qerds.contacts.legalNamePlaceholder")}
              />
            </div>
            <div className="w-[26%]">
              <Input
                value={kvkNumber}
                onChange={(event) => setKvkNumber(event.target.value)}
                placeholder={t("qerds.contacts.kvkPlaceholder")}
                aria-label={t("qerds.contacts.kvkPlaceholder")}
              />
            </div>
            <div className="w-[26%]">
              <Input
                value={euid}
                onChange={(event) => setEuid(event.target.value)}
                placeholder={t("qerds.contacts.euidPlaceholder")}
                aria-label={t("qerds.contacts.euidPlaceholder")}
              />
            </div>
          </div>
          <div className="flex justify-end">
            <Button
              type="submit"
              icon="add"
              disabled={
                name.trim() === "" || address.trim() === "" || create.isPending
              }
            >
              {create.isPending
                ? t("qerds.contacts.adding")
                : t("qerds.contacts.add")}
            </Button>
          </div>
        </form>
        {create.isError && (
          <p role="alert" className="text-error mt-2 text-[13px]">
            {contactError(create.error, t)}
          </p>
        )}

        <div className="mt-5">
          {contacts.isError ? (
            <p className="text-error text-[13px]">
              {t("qerds.contacts.loadError", {
                message: contacts.error.message,
              })}
            </p>
          ) : org.isPending || contacts.isPending ? (
            <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
          ) : contacts.data.length === 0 ? (
            <p className="text-ink-soft text-[13px]">
              {t("qerds.contacts.empty")}
            </p>
          ) : (
            <ul className="rounded-yivi border-line divide-line divide-y border">
              {contacts.data.map((contact) => (
                <li
                  key={contact.id}
                  className="flex items-center gap-2 px-4 py-2.5"
                >
                  <div className="min-w-0 flex-1">
                    <div className="text-ink truncate text-[13.5px]">
                      {contact.name}
                    </div>
                    <div className="text-ink-soft truncate font-mono text-[12px]">
                      {contact.address}
                    </div>
                    {contact.kvkNumber && (
                      <div className="text-ink-soft truncate text-[12px]">
                        {t("qerds.contacts.kvkLabel", {
                          kvk: contact.kvkNumber,
                        })}
                      </div>
                    )}
                  </div>
                  <Button
                    size="sm"
                    variant="danger"
                    icon="delete"
                    onClick={() => remove.mutate({ contactId: contact.id })}
                    disabled={remove.isPending}
                  >
                    {t("qerds.contacts.delete")}
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
        {remove.isError && (
          <p role="alert" className="text-error mt-2 text-[13px]">
            {contactError(remove.error, t)}
          </p>
        )}
      </Card>
    );
  };

  return (
    <>
      <TopBar
        title={t("qerds.contacts.title")}
        subtitle={t("qerds.contacts.subtitle")}
      />
      <div className="p-8">{body()}</div>
    </>
  );
}
