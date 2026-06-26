import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCreateOrganizationMutation } from "../api/organization.queries";
import { ApiError } from "../api/http";
import { Button, Card, Input, TopBar } from "../ui";
import * as React from "react";

const CONFLICT_STATUS = 409;

function errorMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError && error.status === CONFLICT_STATUS) {
    return t("createOrg.slugTaken");
  }
  return t("createOrg.error", { message: error.message });
}

export default function CreateOrganization(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const create = useCreateOrganizationMutation();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    create.mutate(
      { name: name.trim(), slug: slug.trim() },
      { onSuccess: (org) => void navigate(`/${org.slug}`) },
    );
  }

  const canSubmit =
    name.trim() !== "" && slug.trim() !== "" && !create.isPending;

  return (
    <>
      <TopBar title={t("createOrg.title")} subtitle={t("createOrg.subtitle")} />

      <div className="p-8">
        <Card className="max-w-lg p-6">
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <label className="flex flex-col gap-1.5">
              <span className="text-ink text-[13px] font-semibold">
                {t("createOrg.nameLabel")}
              </span>
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t("createOrg.namePlaceholder")}
                autoFocus
              />
            </label>

            <label className="flex flex-col gap-1.5">
              <span className="text-ink text-[13px] font-semibold">
                {t("createOrg.slugLabel")}
              </span>
              <Input
                value={slug}
                onChange={(event) => setSlug(event.target.value)}
                placeholder={t("createOrg.slugPlaceholder")}
                className="font-mono"
              />
            </label>

            {create.isError && (
              <p
                role="alert"
                className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
              >
                {errorMessage(create.error, t)}
              </p>
            )}

            <div className="mt-2 flex gap-2">
              <Button type="submit" disabled={!canSubmit}>
                {create.isPending
                  ? t("createOrg.creating")
                  : t("createOrg.submit")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => void navigate("/admin/organizations")}
              >
                {t("createOrg.cancel")}
              </Button>
            </div>
          </form>
        </Card>
      </div>
    </>
  );
}
