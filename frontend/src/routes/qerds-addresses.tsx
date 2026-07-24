import { useState } from "react";
import { useParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  useCreateQerdsAddressMutation,
  useQerdsAddressesQuery,
  useSetDefaultQerdsAddressMutation,
} from "../api/qerds.queries";
import { useOrganizationQuery } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { accessMessage } from "../lib/access-message";
import { Button, Card, Input, Tag, TopBar } from "../ui";
import * as React from "react";

const CONFLICT_STATUS = 409;
const BAD_REQUEST_STATUS = 400;

function errorCode(error: ApiError): string | null {
  const body = error.body;
  if (typeof body === "object" && body !== null && "code" in body) {
    const code = (body as { code?: unknown }).code;
    return typeof code === "string" ? code : null;
  }
  return null;
}

function addressError(error: Error, t: TFunction): string {
  if (
    error instanceof ApiError &&
    error.status === CONFLICT_STATUS &&
    errorCode(error) === "address_taken"
  ) {
    return t("qerds.addresses.taken");
  }
  if (
    error instanceof ApiError &&
    error.status === BAD_REQUEST_STATUS &&
    errorCode(error) === "address_outside_namespace"
  ) {
    return t("qerds.addresses.outsideNamespace");
  }
  return t("qerds.addresses.error", { message: error.message });
}

export default function QerdsAddresses(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const addresses = useQerdsAddressesQuery(slug, !org.isError);
  const create = useCreateQerdsAddressMutation(slug);
  const setDefault = useSetDefaultQerdsAddressMutation(slug);

  const [localPart, setLocalPart] = useState("");

  function handleCreate(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    const trimmed = localPart.trim();
    create.mutate(
      { localPart: trimmed === "" ? undefined : trimmed },
      { onSuccess: () => setLocalPart("") },
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
          {t("qerds.addresses.heading")}
        </h2>
        <p className="text-ink-soft mt-1 text-[13px]">
          {t("qerds.addresses.description")}
        </p>

        {isAdmin && (
          <>
            <form onSubmit={handleCreate} className="mt-4 flex gap-2">
              <div className="flex-1">
                <Input
                  value={localPart}
                  onChange={(event) => setLocalPart(event.target.value)}
                  placeholder={t("qerds.addresses.localPartPlaceholder", {
                    slug,
                  })}
                  aria-label={t("qerds.addresses.localPartPlaceholder", {
                    slug,
                  })}
                />
              </div>
              <Button type="submit" icon="add" disabled={create.isPending}>
                {create.isPending
                  ? t("qerds.addresses.adding")
                  : t("qerds.addresses.add")}
              </Button>
            </form>
            {create.isError && (
              <p role="alert" className="text-error mt-2 text-[13px]">
                {addressError(create.error, t)}
              </p>
            )}
          </>
        )}

        <div className="mt-5">
          {addresses.isError ? (
            <p className="text-error text-[13px]">
              {t("qerds.addresses.loadError", {
                message: addresses.error.message,
              })}
            </p>
          ) : org.isPending || addresses.isPending ? (
            <p className="text-ink-soft text-[13px]">{t("common.loading")}</p>
          ) : addresses.data.length === 0 ? (
            <p className="text-ink-soft text-[13px]">
              {t("qerds.addresses.empty")}
            </p>
          ) : (
            <ul className="rounded-yivi border-line divide-line divide-y border">
              {addresses.data.map((address) => (
                <li
                  key={address.id}
                  className="flex items-center gap-2 px-4 py-2.5"
                >
                  <span className="text-ink flex-1 truncate font-mono text-[13px]">
                    {address.address}
                  </span>
                  {address.isDefault ? (
                    <Tag tone="green">{t("qerds.addresses.default")}</Tag>
                  ) : (
                    isAdmin && (
                      <Button
                        variant="secondary"
                        onClick={() =>
                          setDefault.mutate({ addressId: address.id })
                        }
                        disabled={setDefault.isPending}
                      >
                        {t("qerds.addresses.setDefault")}
                      </Button>
                    )
                  )}
                </li>
              ))}
            </ul>
          )}
          {setDefault.isError && (
            <p role="alert" className="text-error mt-2 text-[13px]">
              {addressError(setDefault.error, t)}
            </p>
          )}
        </div>
      </Card>
    );
  };

  return (
    <>
      <TopBar
        title={t("qerds.addresses.title")}
        subtitle={t("qerds.addresses.subtitle")}
      />
      <div className="p-8">{body()}</div>
    </>
  );
}
