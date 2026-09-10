import { Navigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import type {
  OpenID4VPOrg,
  OpenID4VPSelectResult,
  OpenID4VPStatus,
} from "../api/openid4vp";
import {
  useOpenID4VPOrgsQuery,
  useOpenID4VPStatusQuery,
  useSelectOpenID4VPOrganizationMutation,
} from "../api/openid4vp.queries";
import { transactionErrorKind } from "../lib/openid4vp-invocation";
import { loginPathFor } from "../lib/return-to";
import {
  Avatar,
  Button,
  Card,
  Icon,
  LanguageSwitcher,
  Logo,
  Outcome,
} from "../ui";

// One inbound presentation transaction, addressed by its opaque id. The row is
// the state machine: this page reads its status and draws the matching step —
// sign in (via /login?returnTo=), pick an organization, or the terminal outcome.
// It is deliberately not under ProtectedRoute, which cannot carry a return
// target: the unauthenticated branch is handled here.
export default function OpenID4VPTransaction(): React.JSX.Element | null {
  const { id } = useParams();
  // Guaranteed by the ":id" route segment this component mounts under.
  const transactionId = id!;

  const me = useMeQuery();
  const status = useOpenID4VPStatusQuery(transactionId);
  const authenticated = me.data != null;
  const pending = status.data?.status === "pending_auth";
  const orgs = useOpenID4VPOrgsQuery(transactionId, authenticated && pending);
  const select = useSelectOpenID4VPOrganizationMutation(transactionId);

  if (status.isPending || me.isPending) {
    return null; // avoid a redirect flash before the session is known
  }

  if (pending && !authenticated) {
    return <Navigate to={loginPathFor(transactionId)} replace />;
  }

  return (
    <div className="mesh-wave relative flex min-h-screen flex-col justify-center">
      <div className="absolute top-4 right-4 sm:top-6 sm:right-6">
        <LanguageSwitcher />
      </div>
      <div className="mx-auto w-full max-w-md px-6 py-12">
        <Card className="w-full p-8">
          <div className="flex justify-center">
            <Logo />
          </div>
          {status.isError ? (
            <TransactionError error={status.error} />
          ) : select.isSuccess ? (
            <SelectOutcome
              result={select.data}
              verifier={status.data.verifier}
            />
          ) : select.isError ? (
            <TransactionError error={select.error} />
          ) : status.data.status === "pending_auth" ? (
            <OrgPicker
              verifier={status.data.verifier}
              orgs={orgs}
              busy={select.isPending}
              onSelect={(slug) => select.mutate(slug)}
            />
          ) : (
            <StatusOutcome status={status.data.status} />
          )}
        </Card>
      </div>
    </div>
  );
}

function OrgPicker({
  verifier,
  orgs,
  busy,
  onSelect,
}: {
  verifier: string;
  orgs: ReturnType<typeof useOpenID4VPOrgsQuery>;
  busy: boolean;
  onSelect: (slug: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  if (orgs.isError) {
    return <TransactionError error={orgs.error} />;
  }
  return (
    <>
      <h1 className="mt-6 text-center text-[22px] font-bold">
        {t("openid4vp.title")}
      </h1>
      <p className="text-ink-soft mt-2 text-center text-[14px]">
        {t("openid4vp.requestedBy", { verifier })}
      </p>
      {busy ? (
        <p className="text-ink-soft mt-6 text-center text-[14px]">
          {t("openid4vp.selecting")}
        </p>
      ) : orgs.isPending ? (
        <p className="text-ink-soft mt-6 text-center text-[14px]">
          {t("common.loading")}
        </p>
      ) : orgs.data.length === 0 ? (
        <p className="text-ink-soft mt-6 text-center text-[14px]">
          {t("openid4vp.noOrgs")}
        </p>
      ) : (
        <>
          <p className="text-ink mt-6 text-center text-[14px] font-semibold">
            {t("openid4vp.pickOrg")}
          </p>
          <div className="mt-3 flex flex-col gap-2">
            {orgs.data.map((org) => (
              <OrgButton key={org.slug} org={org} onSelect={onSelect} />
            ))}
          </div>
        </>
      )}
      <p className="text-muted mt-6 flex items-center justify-center gap-1.5 text-center text-[12.5px]">
        <Icon name="lock" size={13} />
        {t("openid4vp.minimisationHint")}
      </p>
    </>
  );
}

function OrgButton({
  org,
  onSelect,
}: {
  org: OpenID4VPOrg;
  onSelect: (slug: string) => void;
}): React.JSX.Element {
  return (
    <button
      type="button"
      onClick={() => onSelect(org.slug)}
      className="border-line-strong hover:bg-surface-3 rounded-yivi bg-surface flex w-full cursor-pointer items-center gap-3 border px-3 py-2.5 text-left transition-colors"
    >
      <Avatar name={org.name} src={org.logoUri} tone="rose" />
      <div className="min-w-0 flex-1">
        <div className="text-ink truncate text-[14px] font-semibold">
          {org.name}
        </div>
      </div>
      <Icon name="chevron_right" size={16} className="text-muted shrink-0" />
    </button>
  );
}

function SelectOutcome({
  result,
  verifier,
}: {
  result: OpenID4VPSelectResult;
  verifier: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  if (result.status === "completed") {
    return (
      <Outcome
        tone="success"
        icon="valid"
        title={t("openid4vp.completedTitle")}
        message={t("openid4vp.completedHint", { verifier })}
        action={
          result.redirectUri ? (
            <a
              href={result.redirectUri}
              className="bg-primary text-primary-fg hover:bg-primary-hover rounded-yivi inline-flex h-9 items-center px-3.5 text-[13.5px] font-medium"
            >
              {t("openid4vp.returnToVerifier", { verifier })}
            </a>
          ) : undefined
        }
      />
    );
  }
  return <StatusOutcome status={result.status} />;
}

function StatusOutcome({
  status,
}: {
  status: OpenID4VPStatus;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  switch (status) {
    case "pending_auth":
      // Drawn by the org picker, never here.
      return null;
    case "org_selected":
      return (
        <Outcome
          tone="info"
          icon="time"
          title={t("openid4vp.awaitingTitle")}
          message={t("openid4vp.awaitingHint")}
        />
      );
    case "completed":
      return (
        <Outcome
          tone="success"
          icon="valid"
          title={t("openid4vp.completedTitle")}
          message={t("openid4vp.completedDoneHint")}
        />
      );
    case "denied":
      return (
        <Outcome
          tone="error"
          icon="close"
          title={t("openid4vp.deniedTitle")}
          message={t("openid4vp.deniedHint")}
        />
      );
    case "expired":
      return (
        <Outcome
          tone="error"
          icon="time"
          title={t("openid4vp.expiredTitle")}
          message={t("openid4vp.expiredHint")}
        />
      );
  }
}

function TransactionError({ error }: { error: unknown }): React.JSX.Element {
  const { t } = useTranslation();
  const kind = transactionErrorKind(error);
  return (
    <Outcome
      tone="error"
      icon="warning"
      title={t(`openid4vp.errors.${kind}.title`)}
      message={t(`openid4vp.errors.${kind}.hint`)}
      action={
        <Button variant="secondary" onClick={() => window.location.reload()}>
          {t("error.reload")}
        </Button>
      }
    />
  );
}
