import { isRouteErrorResponse, useRouteError } from "react-router";
import { useTranslation } from "react-i18next";
import { Button, Icon, Logo } from "../ui";
import * as React from "react";

function errorDetail(error: unknown): string | null {
  if (isRouteErrorResponse(error)) {
    return `${error.status} ${error.statusText}`;
  }
  if (error instanceof Error) {
    return error.stack ?? `${error.name}: ${error.message}`;
  }
  if (typeof error === "string") {
    return error;
  }
  return null;
}

export default function ErrorBoundary(): React.JSX.Element {
  const { t } = useTranslation();
  const error = useRouteError();

  // Surface the real error (with a source-mapped, clickable stack) in the
  // console regardless of environment — the panel below is dev-only.
  React.useEffect(() => {
    console.error("Unhandled route error:", error);
  }, [error]);

  const detail = errorDetail(error);

  return (
    <div className="bg-surface-2 flex min-h-screen flex-col items-center justify-center p-6">
      <main className="flex w-full max-w-lg flex-col items-center text-center">
        <Logo />

        <span
          aria-hidden="true"
          className="bg-error-bg text-error mt-10 inline-flex h-20 w-20 items-center justify-center rounded-full"
        >
          <Icon name="warning" size={36} />
        </span>

        <h1 className="font-display text-ink mt-6 text-[22px] font-bold">
          {t("error.title")}
        </h1>
        <p className="text-ink-soft mt-2 max-w-sm text-[14px] leading-relaxed">
          {t("error.body")}
        </p>

        {import.meta.env.DEV && detail && (
          <pre className="border-line bg-surface text-ink-soft rounded-yivi mt-5 max-h-64 w-full overflow-auto border px-3 py-2.5 text-left font-mono text-[11.5px] leading-relaxed whitespace-pre-wrap">
            {detail}
          </pre>
        )}

        <div className="mt-8 flex w-full flex-col gap-2.5 sm:w-auto sm:flex-row sm:justify-center">
          <Button onClick={() => window.location.reload()}>
            {t("error.reload")}
          </Button>
          <Button
            variant="secondary"
            icon="arrow_back"
            onClick={() => window.location.assign("/")}
          >
            {t("error.home")}
          </Button>
        </div>
      </main>
    </div>
  );
}
