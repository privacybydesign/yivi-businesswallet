import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { REGISTER_SESSION_URL, registerWallet } from "../api/wallet";
import type { WalletEnrollment } from "../api/wallet";
import { meQueryKey } from "../api/auth.queries";
import { representationLabel } from "../lib/representation";
import { Button, Card, IdentityDisclosure, Input, Logo, Outcome } from "../ui";
import * as React from "react";

const FORBIDDEN_STATUS = 403;
const CONFLICT_STATUS = 409;

type Phase = "form" | "disclosing" | "registering" | "done" | "error";

function errorMessage(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) {
    if (error.status === FORBIDDEN_STATUS) {
      return t("register.notRepresentative");
    }
    if (error.status === CONFLICT_STATUS) {
      return t("register.alreadyRegistered");
    }
  }
  return t("register.error");
}

export default function Register(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<Phase>("form");
  const [kvkNumber, setKvkNumber] = useState("");
  const [result, setResult] = useState<WalletEnrollment | null>(null);
  const [message, setMessage] = useState("");

  const onDisclosed = (disclosureToken: string): void => {
    setPhase("registering");
    registerWallet(disclosureToken, kvkNumber.trim())
      .then((data) => {
        setResult(data);
        setPhase("done");
      })
      .catch((error: unknown) => {
        setMessage(errorMessage(error, t));
        setPhase("error");
      });
  };

  // The register response set the session cookie; refetch `me` so the protected
  // routes see the authenticated user before we navigate into the new org.
  const enterApp = async (): Promise<void> => {
    if (result == null) return;
    await queryClient.refetchQueries({ queryKey: meQueryKey });
    void navigate(`/${result.organizationSlug}`);
  };

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-md p-8">
        <div className="flex justify-center">
          <Logo />
        </div>

        {phase === "done" && result != null ? (
          <Outcome
            tone="success"
            icon="valid"
            title={t("register.registeredAt", { org: result.legalName })}
            message={t("register.yourRole", {
              role: representationLabel(result, t),
            })}
            action={
              <Button variant="primary" onClick={() => void enterApp()}>
                {t("register.enter", { org: result.legalName })}
              </Button>
            }
          />
        ) : phase === "error" ? (
          <Outcome
            tone="error"
            icon="warning"
            title={t("register.title")}
            message={message}
            action={
              <Button
                variant="secondary"
                onClick={() => {
                  setMessage("");
                  setPhase("form");
                }}
              >
                {t("register.back")}
              </Button>
            }
          />
        ) : phase === "disclosing" || phase === "registering" ? (
          <>
            <h1 className="mt-6 text-center text-[22px] font-bold">
              {t("register.scanTitle")}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("register.scanPrompt", { kvk: kvkNumber })}
            </p>
            {phase === "registering" ? (
              <p className="text-ink-soft mt-6 text-center text-[14px]">
                {t("register.registering")}
              </p>
            ) : (
              <div className="mt-6 flex justify-center">
                <IdentityDisclosure
                  sessionUrl={REGISTER_SESSION_URL}
                  onToken={onDisclosed}
                  onAborted={() => setPhase("form")}
                />
              </div>
            )}
          </>
        ) : (
          <>
            <h1 className="mt-6 text-center text-[24px] font-bold">
              {t("register.title")}
            </h1>
            <p className="text-ink-soft mt-1 text-center text-[14px]">
              {t("register.subtitle")}
            </p>
            <p className="text-ink-soft mt-4 text-[13.5px] leading-relaxed">
              {t("register.intro")}
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                setPhase("disclosing");
              }}
              className="mt-5 flex flex-col gap-4"
            >
              <label className="flex flex-col gap-1.5">
                <span className="text-ink text-[13px] font-semibold">
                  {t("register.kvkNumber")}
                </span>
                <Input
                  value={kvkNumber}
                  onChange={(event) => setKvkNumber(event.target.value)}
                  placeholder={t("register.kvkPlaceholder")}
                  inputMode="numeric"
                  className="font-mono"
                  autoFocus
                />
              </label>
              <Button type="submit" disabled={kvkNumber.trim() === ""}>
                {t("register.continue")}
              </Button>
            </form>
            <p className="text-ink-soft mt-6 text-center text-[13px]">
              <button
                type="button"
                onClick={() => void navigate("/login")}
                className="text-primary font-medium hover:underline"
              >
                {t("register.haveAccount")}
              </button>
            </p>
          </>
        )}
      </Card>
    </div>
  );
}
