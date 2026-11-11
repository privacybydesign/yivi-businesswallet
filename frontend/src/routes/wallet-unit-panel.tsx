import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useActivateWalletUnitMutation,
  useRevokeWalletUnitMutation,
  useWalletUnitQuery,
} from "../api/wallet-unit.queries";
import { WALLET_UNIT_REVOKE_REASONS } from "../api/wallet-unit";
import type { WalletUnitRevokeReason } from "../api/wallet-unit";
import { useWhenFormatter } from "../lib/format-when";
import { WALLET_UNIT_TONE, walletUnitState } from "../lib/wallet-unit";
import { Button, Card, ConfirmDialog, Select, Tag } from "../ui";

const EYEBROW =
  "text-muted font-label text-[11px] font-medium tracking-[0.06em] uppercase";

export function WalletUnitPanel({
  slug,
}: Readonly<{ slug: string }>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const view = useWalletUnitQuery(slug);
  const revoke = useRevokeWalletUnitMutation(slug);
  const activate = useActivateWalletUnitMutation(slug);
  const [reason, setReason] = useState<WalletUnitRevokeReason>("owner_request");
  const [confirming, setConfirming] = useState(false);

  if (view.isPending) {
    return (
      <Card className="max-w-2xl p-7">
        <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
      </Card>
    );
  }
  if (view.isError) {
    return (
      <Card className="max-w-2xl p-7">
        <p role="alert" className="text-error text-[14px]">
          {t("walletUnit.loadError", { message: view.error.message })}
        </p>
      </Card>
    );
  }

  const state = walletUnitState(view.data);
  const summary = view.data.summary;
  const actionError = revoke.error ?? activate.error;

  return (
    <Card className="max-w-2xl p-7">
      <div className="flex items-center gap-2">
        <h2 className="text-[16px] font-semibold">{t("walletUnit.title")}</h2>
        <Tag tone={WALLET_UNIT_TONE[state]}>
          {t(`walletUnit.states.${state}`)}
        </Tag>
      </div>
      <p className="text-ink-soft mt-1 text-[13px]">{t("walletUnit.intro")}</p>
      {summary && (
        <div className="mt-4 grid grid-cols-[180px_1fr] items-center gap-x-5 gap-y-3.5">
          <span className={EYEBROW}>{t("walletUnit.createdAt")}</span>
          <span className="text-ink text-[13px]">
            {formatWhen(summary.unit.createdAt)}
          </span>
          <span className={EYEBROW}>{t("walletUnit.issuedCount")}</span>
          <span className="text-ink text-[13px]">{summary.issuedCount}</span>
          {summary.lastIssuedAt && (
            <>
              <span className={EYEBROW}>{t("walletUnit.lastIssuedAt")}</span>
              <span className="text-ink text-[13px]">
                {formatWhen(summary.lastIssuedAt)}
              </span>
            </>
          )}
          {summary.unit.revokedAt && (
            <>
              <span className={EYEBROW}>{t("walletUnit.revokedAt")}</span>
              <span className="text-ink text-[13px]">
                {formatWhen(summary.unit.revokedAt)}
                {summary.unit.revokedReason &&
                  ` · ${t(`walletUnit.reasons.${summary.unit.revokedReason}`)}`}
              </span>
            </>
          )}
        </div>
      )}
      {state === "active" && (
        <div className="mt-5 flex items-center gap-3">
          <Select
            className="w-64"
            value={reason}
            aria-label={t("walletUnit.reason")}
            onChange={(event) =>
              setReason(event.target.value as WalletUnitRevokeReason)
            }
          >
            {WALLET_UNIT_REVOKE_REASONS.map((r) => (
              <option key={r} value={r}>
                {t(`walletUnit.reasons.${r}`)}
              </option>
            ))}
          </Select>
          <Button
            variant="danger"
            size="sm"
            onClick={() => setConfirming(true)}
          >
            {t("walletUnit.revoke")}
          </Button>
        </div>
      )}
      {state === "revoked" && (
        <div className="mt-5">
          <p className="text-ink-soft mb-3 text-[13px]">
            {t("walletUnit.activateHint")}
          </p>
          <Button
            size="sm"
            disabled={activate.isPending}
            onClick={() => activate.mutate()}
          >
            {t("walletUnit.activate")}
          </Button>
        </div>
      )}
      {actionError && (
        <p role="alert" className="text-error mt-3 text-[13px]">
          {t("walletUnit.actionError", { message: actionError.message })}
        </p>
      )}
      {confirming && (
        <ConfirmDialog
          title={t("walletUnit.revoke")}
          message={t("walletUnit.confirmRevoke")}
          confirmLabel={t("walletUnit.revoke")}
          busy={revoke.isPending}
          onConfirm={() => {
            revoke.mutate({ reason });
            setConfirming(false);
          }}
          onClose={() => setConfirming(false)}
        />
      )}
    </Card>
  );
}
