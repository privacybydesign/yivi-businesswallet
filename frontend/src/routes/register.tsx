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
import {
  Button,
  Card,
  DataDisclosure,
  IdentityDisclosure,
  Input,
  Logo,
  Outcome,
  Stepper,
} from "../ui";
import type { DataDisclosureItem } from "../ui";
import * as React from "react";

const FORBIDDEN_STATUS = 403;
const CONFLICT_STATUS = 409;

type Phase = "form" | "disclosing" | "registering" | "done" | "error";

function errorCode(error: ApiError): string | null {
  if (error.body && typeof error.body === "object" && "code" in error.body) {
    const { code } = error.body as { code?: unknown };
    return typeof code === "string" ? code : null;
  }
  return null;
}

function errorMessage(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) {
    const code = errorCode(error);
    if (error.status === FORBIDDEN_STATUS) {
      return t("register.notRepresentative");
    }
    if (code === "slug_taken") {
      return t("register.slugTaken");
    }
    if (code === "reserved_slug" || code === "invalid_slug") {
      return t("register.slugInvalid");
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
  const [slug, setSlug] = useState("");
  const [result, setResult] = useState<WalletEnrollment | null>(null);
  const [message, setMessage] = useState("");

  const onDisclosed = (disclosureToken: string): void => {
    setPhase("registering");
    registerWallet({
      disclosureToken,
      kvkNumber: kvkNumber.trim(),
      slug: slug.trim(),
    })
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

  const steps = [
    t("register.steps.kvk"),
    t("register.steps.identity"),
    t("register.steps.onboard"),
  ];
  // Map each phase to the active step. `done` sits past the last index so every
  // step reads complete on the confirmation screen; `error` is unused (the error
  // screen hides the stepper).
  const stepForPhase: Record<Phase, number> = {
    form: 0,
    disclosing: 1,
    registering: 2,
    done: 3,
    error: 2,
  };
  // What the wallet discloses on the "Prove your identity" step — mirrors the
  // backend's identity scope (passport OR id-card + email + phone).
  const disclosureItems: DataDisclosureItem[] = [
    {
      icon: "personal",
      label: t("register.disclosure.identityLabel"),
      detail: t("register.disclosure.identityDetail"),
    },
    {
      icon: "email",
      label: t("register.disclosure.emailLabel"),
      detail: t("register.disclosure.emailDetail"),
    },
    {
      icon: "phone",
      label: t("register.disclosure.phoneLabel"),
      detail: t("register.disclosure.phoneDetail"),
    },
  ];

  return (
    <div className="bg-surface-2 flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-lg">
        <div className="mb-6 flex justify-center">
          <Logo />
        </div>
        <Card className="p-8">
          {phase !== "error" && (
            <Stepper steps={steps} current={stepForPhase[phase]} />
          )}
          <div className={phase === "error" ? "" : "mt-8"}>
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
            ) : phase === "registering" ? (
              <>
                <h1 className="text-center text-[20px] font-bold">
                  {t("register.onboardTitle")}
                </h1>
                <div className="mt-8 flex flex-col items-center gap-4">
                  <span
                    aria-hidden="true"
                    className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
                  />
                  <p className="text-ink-soft text-[14px]">
                    {t("register.registering")}
                  </p>
                </div>
              </>
            ) : phase === "disclosing" ? (
              <>
                <h1 className="text-center text-[20px] font-bold">
                  {t("register.identityTitle")}
                </h1>
                <p className="text-ink-soft mt-2 text-center text-[13.5px] leading-relaxed">
                  {t("register.identityIntro")}
                </p>
                <div className="border-line bg-surface-2 rounded-yivi mt-5 border p-4">
                  <DataDisclosure items={disclosureItems} />
                </div>
                <p className="text-muted mt-3 text-[12px] leading-snug">
                  {t("register.disclosureFootnote")}
                </p>
                <div className="mt-6 flex justify-center">
                  <IdentityDisclosure
                    sessionUrl={REGISTER_SESSION_URL}
                    onToken={onDisclosed}
                    onAborted={() => setPhase("form")}
                  />
                </div>
              </>
            ) : (
              <>
                <h1 className="text-center text-[22px] font-bold">
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
                  <label className="flex flex-col gap-1.5">
                    <span className="text-ink text-[13px] font-semibold">
                      {t("register.slug")}
                    </span>
                    <Input
                      value={slug}
                      onChange={(event) => setSlug(event.target.value)}
                      placeholder={t("register.slugPlaceholder")}
                      className="font-mono"
                    />
                    <span className="text-muted text-[12px]">
                      {t("register.slugHint")}
                    </span>
                  </label>
                  <Button
                    type="submit"
                    disabled={kvkNumber.trim() === "" || slug.trim() === ""}
                  >
                    {t("register.continue")}
                  </Button>
                </form>
              </>
            )}
          </div>
        </Card>
        {phase === "form" && (
          <p className="text-ink-soft mt-6 text-center text-[13px]">
            <button
              type="button"
              onClick={() => void navigate("/login")}
              className="text-primary font-medium hover:underline"
            >
              {t("register.haveAccount")}
            </button>
          </p>
        )}
      </div>
    </div>
  );
}
