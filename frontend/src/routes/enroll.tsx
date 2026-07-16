import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useEnrollWalletMutation } from "../api/wallet.queries";
import type { WalletEnrollment } from "../api/wallet";
import { ApiError } from "../api/http";
import { representationLabel } from "../lib/representation";
import { Button, Card, Input, Outcome, TopBar } from "../ui";
import * as React from "react";

const FORBIDDEN_STATUS = 403;

function errorCode(error: ApiError): string | null {
  const body = error.body;
  if (typeof body === "object" && body !== null && "code" in body) {
    const code = (body as { code?: unknown }).code;
    return typeof code === "string" ? code : null;
  }
  return null;
}

const CONFLICT_STATUS = 409;

function errorMessage(error: Error, t: TFunction): string {
  if (error instanceof ApiError) {
    if (
      error.status === FORBIDDEN_STATUS &&
      errorCode(error) === "not_a_representative"
    ) {
      return t("enroll.notRepresentative");
    }
    if (
      error.status === CONFLICT_STATUS &&
      errorCode(error) === "already_registered"
    ) {
      return t("enroll.alreadyRegistered");
    }
  }
  return t("enroll.error", { message: error.message });
}

export default function Enroll(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const enroll = useEnrollWalletMutation();
  const [kvkNumber, setKvkNumber] = useState("");
  const [result, setResult] = useState<WalletEnrollment | null>(null);

  function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    enroll.mutate(kvkNumber.trim(), {
      onSuccess: (data) => setResult(data),
    });
  }

  if (result !== null) {
    return (
      <>
        <TopBar title={t("enroll.title")} />
        <div className="p-8">
          <Card className="max-w-lg p-8">
            <Outcome
              tone="success"
              icon="valid"
              title={t("enroll.registeredAt", { org: result.legalName })}
              message={t("enroll.yourRole", {
                role: representationLabel(result, t),
              })}
              action={
                <Button
                  variant="primary"
                  onClick={() => void navigate(`/${result.organizationSlug}`)}
                >
                  {t("enroll.continue", { org: result.legalName })}
                </Button>
              }
            />
          </Card>
        </div>
      </>
    );
  }

  const canSubmit = kvkNumber.trim() !== "" && !enroll.isPending;

  return (
    <>
      <TopBar title={t("enroll.title")} subtitle={t("enroll.subtitle")} />

      <div className="p-8">
        <Card className="max-w-lg p-6">
          <p className="text-ink-soft mb-5 text-[13.5px] leading-relaxed">
            {t("enroll.intro")}
          </p>

          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <label className="flex flex-col gap-1.5">
              <span className="text-ink text-[13px] font-semibold">
                {t("enroll.kvkNumber")}
              </span>
              <Input
                value={kvkNumber}
                onChange={(event) => setKvkNumber(event.target.value)}
                placeholder={t("enroll.kvkPlaceholder")}
                inputMode="numeric"
                className="font-mono"
                autoFocus
              />
            </label>

            {enroll.isError && (
              <p
                role="alert"
                className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
              >
                {errorMessage(enroll.error, t)}
              </p>
            )}

            <div className="mt-2">
              <Button type="submit" disabled={!canSubmit}>
                {enroll.isPending ? t("enroll.consulting") : t("enroll.submit")}
              </Button>
            </div>
          </form>
        </Card>
      </div>
    </>
  );
}
