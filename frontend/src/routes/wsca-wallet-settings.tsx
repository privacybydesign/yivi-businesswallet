import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useActivateWscaMutation,
  useRotateWscaMutation,
  useWscaStatusQuery,
} from "../api/wsca.queries";
import type { WscaAccount } from "../api/wsca";
import { useWhenFormatter } from "../lib/format-when";
import { Button, Card } from "../ui";

const EYEBROW =
  "text-muted font-mono text-[11px] font-medium tracking-[0.06em] uppercase";
const CONTROL =
  "rounded-yivi border-line-strong bg-surface text-ink h-9 w-full border px-3 text-[13.5px] outline-none transition-colors focus:border-ink focus:ring-ink/10 focus:ring-3";

// Mirrors the backend WSCA PIN policy (secdsa.MinPINLength): at least 5 digits.
const SECRET_PATTERN = /^\d{5,}$/;

export function WscaWalletPanel({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const status = useWscaStatusQuery(slug);

  if (status.isPending) {
    return (
      <Card className="max-w-2xl p-7">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  if (status.isError) {
    return (
      <Card className="max-w-2xl p-7">
        <p role="alert" className="text-error text-[14px]">
          {t("wscaWallet.loadError", { message: status.error.message })}
        </p>
      </Card>
    );
  }
  if (!status.data.configured) {
    return (
      <Card className="max-w-2xl p-7">
        <h2 className="text-[16px] font-semibold">{t("wscaWallet.title")}</h2>
        <p className="text-ink-soft mt-1 text-[13px]">
          {t("wscaWallet.notConfigured")}
        </p>
      </Card>
    );
  }

  return (
    <div className="flex max-w-2xl flex-col gap-6">
      {status.data.activated && status.data.account ? (
        <>
          <AccountCard account={status.data.account} />
          <RotateForm slug={slug} />
        </>
      ) : (
        <ActivateForm slug={slug} />
      )}
    </div>
  );
}

function AccountCard({ account }: { account: WscaAccount }): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  return (
    <Card className="p-7">
      <div className="flex items-center gap-2">
        <h2 className="text-[16px] font-semibold">{t("wscaWallet.title")}</h2>
        <span className="rounded-yivi bg-success-bg text-success px-2 py-0.5 text-[11px] font-medium">
          {t("wscaWallet.statusActivated")}
        </span>
      </div>
      <p className="text-ink-soft mt-1 text-[13px]">{t("wscaWallet.intro")}</p>
      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <span className={EYEBROW}>{t("wscaWallet.accountId")}</span>
        <span className="text-ink font-mono text-[13px] break-all">
          {account.accountId}
        </span>
        <span className={EYEBROW}>{t("wscaWallet.certificateId")}</span>
        <span className="text-ink font-mono text-[13px] break-all">
          {account.certificateId}
        </span>
        <span className={EYEBROW}>{t("wscaWallet.activatedAt")}</span>
        <span className="text-ink text-[13px]">
          {formatWhen(account.activatedAt)}
        </span>
        {account.rotatedAt && (
          <>
            <span className={EYEBROW}>{t("wscaWallet.rotatedAt")}</span>
            <span className="text-ink text-[13px]">
              {formatWhen(account.rotatedAt)}
            </span>
          </>
        )}
      </div>
    </Card>
  );
}

function ActivateForm({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const activate = useActivateWscaMutation(slug);
  const [secret, setSecret] = useState("");
  const [confirm, setConfirm] = useState("");
  const [attempted, setAttempted] = useState(false);

  const secretInvalid = attempted && !SECRET_PATTERN.test(secret);
  const mismatch = attempted && secret !== confirm;

  function handleActivate(): void {
    setAttempted(true);
    if (
      !SECRET_PATTERN.test(secret) ||
      secret !== confirm ||
      activate.isPending
    ) {
      return;
    }
    activate.mutate({ secret });
  }

  return (
    <Card className="p-7">
      <h2 className="text-[16px] font-semibold">{t("wscaWallet.title")}</h2>
      <p className="text-ink-soft mt-1 text-[13px]">{t("wscaWallet.intro")}</p>
      <p className="text-ink-soft mt-2 text-[13px]">
        {t("wscaWallet.activateHint")}
      </p>
      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <span className={EYEBROW}>{t("wscaWallet.secret")}</span>
        <input
          type="password"
          className={`${CONTROL} font-mono`}
          value={secret}
          onChange={(event) => setSecret(event.target.value)}
          autoComplete="new-password"
          aria-label={t("wscaWallet.secret")}
        />
        <span className={EYEBROW}>{t("wscaWallet.confirmSecret")}</span>
        <input
          type="password"
          className={`${CONTROL} font-mono`}
          value={confirm}
          onChange={(event) => setConfirm(event.target.value)}
          autoComplete="new-password"
          aria-label={t("wscaWallet.confirmSecret")}
        />
      </div>
      <p className="text-ink-soft mt-2 text-[12px]">
        {t("wscaWallet.secretHint")}
      </p>
      {secretInvalid && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("wscaWallet.secretInvalid")}
        </p>
      )}
      {!secretInvalid && mismatch && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("wscaWallet.secretMismatch")}
        </p>
      )}
      <div className="mt-5">
        <Button onClick={handleActivate} disabled={activate.isPending}>
          {activate.isPending
            ? t("wscaWallet.activating")
            : t("wscaWallet.activate")}
        </Button>
      </div>
      {activate.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {t("wscaWallet.actionError", { message: activate.error.message })}
        </p>
      )}
    </Card>
  );
}

function RotateForm({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const rotate = useRotateWscaMutation(slug);
  const [currentSecret, setCurrentSecret] = useState("");
  const [newSecret, setNewSecret] = useState("");
  const [confirm, setConfirm] = useState("");
  const [attempted, setAttempted] = useState(false);

  const newInvalid = attempted && !SECRET_PATTERN.test(newSecret);
  const mismatch = attempted && newSecret !== confirm;
  const missingCurrent = attempted && currentSecret === "";

  function handleRotate(): void {
    setAttempted(true);
    if (
      currentSecret === "" ||
      !SECRET_PATTERN.test(newSecret) ||
      newSecret !== confirm ||
      rotate.isPending
    ) {
      return;
    }
    rotate.mutate(
      { currentSecret, newSecret },
      {
        onSuccess: () => {
          setCurrentSecret("");
          setNewSecret("");
          setConfirm("");
          setAttempted(false);
        },
      },
    );
  }

  return (
    <Card className="p-7">
      <h2 className="text-[16px] font-semibold">
        {t("wscaWallet.rotateTitle")}
      </h2>
      <p className="text-ink-soft mt-1 text-[13px]">
        {t("wscaWallet.rotateHint")}
      </p>
      <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
        <span className={EYEBROW}>{t("wscaWallet.currentSecret")}</span>
        <input
          type="password"
          className={`${CONTROL} font-mono`}
          value={currentSecret}
          onChange={(event) => setCurrentSecret(event.target.value)}
          autoComplete="current-password"
          aria-label={t("wscaWallet.currentSecret")}
        />
        <span className={EYEBROW}>{t("wscaWallet.newSecret")}</span>
        <input
          type="password"
          className={`${CONTROL} font-mono`}
          value={newSecret}
          onChange={(event) => setNewSecret(event.target.value)}
          autoComplete="new-password"
          aria-label={t("wscaWallet.newSecret")}
        />
        <span className={EYEBROW}>{t("wscaWallet.confirmSecret")}</span>
        <input
          type="password"
          className={`${CONTROL} font-mono`}
          value={confirm}
          onChange={(event) => setConfirm(event.target.value)}
          autoComplete="new-password"
          aria-label={t("wscaWallet.confirmSecret")}
        />
      </div>
      {missingCurrent && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("wscaWallet.currentRequired")}
        </p>
      )}
      {!missingCurrent && newInvalid && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("wscaWallet.secretInvalid")}
        </p>
      )}
      {!missingCurrent && !newInvalid && mismatch && (
        <p role="alert" className="text-error mt-2 text-[12px]">
          {t("wscaWallet.secretMismatch")}
        </p>
      )}
      <div className="mt-5">
        <Button
          variant="secondary"
          onClick={handleRotate}
          disabled={rotate.isPending}
        >
          {rotate.isPending ? t("wscaWallet.rotating") : t("wscaWallet.rotate")}
        </Button>
      </div>
      {rotate.isError && (
        <p role="alert" className="text-error mt-2 text-[13px]">
          {t("wscaWallet.actionError", { message: rotate.error.message })}
        </p>
      )}
    </Card>
  );
}
