import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { Button, Logo } from "../ui";
import * as React from "react";

export default function NotFound(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();

  return (
    <div className="bg-surface-2 flex min-h-screen flex-col items-center justify-center p-6">
      <main className="flex w-full max-w-md flex-col items-center text-center">
        <Logo />

        <p
          aria-hidden="true"
          className="font-display text-ink mt-10 text-[96px] leading-none font-extrabold tracking-[-0.04em] select-none sm:text-[132px]"
        >
          4<span className="text-brand">0</span>4
        </p>

        <h1 className="font-display text-ink mt-6 text-[22px] font-bold">
          {t("notFound.title")}
        </h1>
        <p className="text-ink-soft mt-2 max-w-sm text-[14px] leading-relaxed">
          {t("notFound.body")}
        </p>

        <div className="mt-8 flex w-full flex-col gap-2.5 sm:w-auto sm:flex-row sm:justify-center">
          <Button onClick={() => void navigate("/")}>
            {t("notFound.home")}
          </Button>
          <Button
            variant="secondary"
            icon="arrow_back"
            onClick={() => void navigate(-1)}
          >
            {t("notFound.back")}
          </Button>
        </div>
      </main>
    </div>
  );
}
