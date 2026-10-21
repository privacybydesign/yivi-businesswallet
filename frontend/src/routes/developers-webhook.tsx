import { useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useWebhookDeliveriesQuery,
  useProofingWebhookQuery,
  useRemoveWebhookMutation,
  useNewWebhookSecretMutation,
  useSaveWebhookMutation,
  useSendWebhookTestMutation,
} from "../api/identity-proofing.queries";
import type {
  ProofingWebhook,
  WebhookDelivery,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  isSuccessStatus,
  ownEndpointDeliveries,
  proofingErrorMessage,
  shortRequestId,
} from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Input, StatusText } from "../ui";
import { SecretReveal, WebhookStateText } from "./proofing-ui";

const ERROR = "text-error text-[12.5px]";
const HINT = "text-muted text-[12px]";
const CAPTION =
  "text-muted font-label text-[10.5px] font-medium tracking-[0.08em] uppercase";
// The masked middle of a secret whose last four characters are shown.
const SECRET_MASK = "••••••••••••••••";

// Where the org's session results go: one endpoint the org hosts, for all its
// flows, with the deliveries made to it. Without one, no event reaches the org.
export function WebhookSection({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const webhook = useProofingWebhookQuery(slug);
  const [editing, setEditing] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);

  if (webhook.isPending) {
    return <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>;
  }
  if (webhook.isError) {
    return <p className={ERROR}>{proofingErrorMessage(webhook.error, t)}</p>;
  }
  const hook = webhook.data;
  return (
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
      {!hook.configured && !editing && (
        <DefaultEndpointCard onCustomize={() => setEditing(true)} />
      )}
      {editing && (
        <EndpointForm
          slug={slug}
          webhook={hook}
          onCancel={() => setEditing(false)}
          onSaved={(saved) => {
            setEditing(false);
            if (saved.secret) {
              setSecret(saved.secret);
            }
          }}
        />
      )}
      {hook.configured && !editing && (
        <EndpointCard
          slug={slug}
          webhook={hook}
          onEdit={() => setEditing(true)}
          onRotated={setSecret}
        />
      )}
      <DeliveriesCard slug={slug} />
      {secret && (
        <SecretReveal
          title={t("identityProofing.webhooks.secretTitle")}
          hint={t("identityProofing.webhooks.secretHint")}
          secret={secret}
          onClose={() => setSecret(null)}
        />
      )}
    </div>
  );
}

function EndpointForm({
  slug,
  webhook,
  onCancel,
  onSaved,
}: {
  slug: string;
  webhook: ProofingWebhook;
  onCancel?: () => void;
  onSaved: (saved: ProofingWebhook) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveWebhookMutation(slug);
  const [url, setUrl] = useState(webhook.url ?? "");
  const [events, setEvents] = useState<ReadonlySet<string>>(
    () =>
      new Set(webhook.configured ? webhook.events : webhook.availableEvents),
  );

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    save.mutate(
      { url: url.trim(), events: [...events] },
      { onSuccess: onSaved },
    );
  }

  return (
    <Card>
      <form onSubmit={submit} noValidate>
        <div className="border-line border-b px-5 py-4">
          <h2 className="font-display text-[17px] font-bold">
            {t("identityProofing.webhooks.endpoint")}
          </h2>
          <p className={`${HINT} mt-1`}>
            {t("identityProofing.webhooks.formHint")}
          </p>
        </div>
        <div className="flex flex-col gap-4 px-5 py-4">
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="webhook-url"
              className="text-ink text-[13px] font-semibold"
            >
              {t("identityProofing.webhooks.url")}
            </label>
            <Input
              id="webhook-url"
              type="url"
              value={url}
              className="font-mono"
              placeholder={t("identityProofing.webhooks.urlPlaceholder")}
              onChange={(e) => setUrl(e.target.value)}
            />
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-ink mb-1 text-[13px] font-semibold">
              {t("identityProofing.webhooks.events")}
            </legend>
            {webhook.availableEvents.map((name) => (
              <label key={name} className="flex items-center gap-2">
                <input
                  type="checkbox"
                  className="h-4 w-4"
                  checked={events.has(name)}
                  onChange={(e) =>
                    setEvents((current) => {
                      const next = new Set(current);
                      if (e.target.checked) next.add(name);
                      else next.delete(name);
                      return next;
                    })
                  }
                />
                <span className="font-mono text-[12.5px]">{name}</span>
              </label>
            ))}
          </fieldset>
          {save.isError && (
            <p className={ERROR}>{proofingErrorMessage(save.error, t)}</p>
          )}
          <div className="flex gap-2">
            <Button
              type="submit"
              loading={save.isPending}
              disabled={url.trim() === "" || events.size === 0}
            >
              {t("identityProofing.webhooks.save")}
            </Button>
            {onCancel && (
              <Button type="button" variant="secondary" onClick={onCancel}>
                {t("common.cancel")}
              </Button>
            )}
          </div>
        </div>
      </form>
    </Card>
  );
}

// Without its own endpoint the org is sent no events; its results show in
// Sessions, through the API and in the audit log.
function DefaultEndpointCard({
  onCustomize,
}: {
  onCustomize: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Card>
      <h2 className="font-display border-line border-b px-5 py-4 text-[17px] font-bold">
        {t("identityProofing.webhooks.endpoint")}
      </h2>
      <div className="flex flex-col gap-3 px-5 py-4">
        <p className="text-ink text-[14px]">
          {t("identityProofing.webhooks.defaultBody")}
        </p>
        <p className="text-ink-soft text-[13px]">
          {t("identityProofing.webhooks.defaultHint")}
        </p>
        <div>
          <Button size="sm" variant="secondary" onClick={onCustomize}>
            {t("identityProofing.webhooks.useOwn")}
          </Button>
        </div>
      </div>
    </Card>
  );
}

function EndpointCard({
  slug,
  webhook,
  onEdit,
  onRotated,
}: {
  slug: string;
  webhook: ProofingWebhook;
  onEdit: () => void;
  onRotated: (secret: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const test = useSendWebhookTestMutation(slug);
  const rotate = useNewWebhookSecretMutation(slug);
  const remove = useRemoveWebhookMutation(slug);
  const [confirm, setConfirm] = useState<"rotate" | "remove" | null>(null);
  const failing = webhook.health.state === "failing";

  return (
    <Card>
      <div className="border-line flex items-center justify-between gap-3 border-b px-5 py-4">
        <h2 className="font-display text-[17px] font-bold">
          {t("identityProofing.webhooks.endpoint")}
        </h2>
        {failing ? (
          <StatusText tone="red">
            {t("identityProofing.webhooks.failing")}
          </StatusText>
        ) : (
          <StatusText tone="green">
            {t("identityProofing.webhooks.delivering")}
          </StatusText>
        )}
      </div>
      <dl className="divide-line divide-y px-5">
        <div className="py-3">
          <dt className={CAPTION}>{t("identityProofing.webhooks.url")}</dt>
          <dd className="text-ink mt-1 font-mono text-[12.5px] break-all">
            {webhook.url}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("identityProofing.webhooks.events")}</dt>
          <dd className="text-ink mt-1 font-mono text-[12.5px]">
            {webhook.events.join(", ")}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("identityProofing.webhooks.secret")}</dt>
          <dd className="text-ink mt-1 font-mono text-[12.5px]">
            {`whsec_${SECRET_MASK}${webhook.secretLast4 ?? ""}`}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("identityProofing.webhooks.retries")}</dt>
          <dd className="text-ink mt-1 text-[13px]">
            {t("identityProofing.webhooks.retriesValue", {
              count: webhook.maxAttempts,
            })}
          </dd>
          {failing && (
            <dd className="mt-1 text-[12.5px]">
              <WebhookStateText webhook={webhook.health} />
              {webhook.health.pendingRetries > 0 &&
                ` · ${t("identityProofing.webhooks.queued", {
                  count: webhook.health.pendingRetries,
                })}`}
            </dd>
          )}
        </div>
      </dl>
      {test.isError && (
        <p className={`${ERROR} px-5`}>{proofingErrorMessage(test.error, t)}</p>
      )}
      <div className="flex flex-wrap gap-2 px-5 pt-2 pb-4">
        <Button
          size="sm"
          variant="secondary"
          loading={test.isPending}
          onClick={() => test.mutate()}
        >
          {t("identityProofing.webhooks.test")}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirm("rotate")}>
          {t("identityProofing.webhooks.rotate")}
        </Button>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={onEdit}>
          {t("identityProofing.webhooks.edit")}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirm("remove")}>
          {t("identityProofing.webhooks.remove")}
        </Button>
      </div>
      {confirm === "rotate" && (
        <ConfirmDialog
          title={t("identityProofing.webhooks.rotateConfirm.title")}
          message={t("identityProofing.webhooks.rotateConfirm.message")}
          confirmLabel={t("identityProofing.webhooks.rotate")}
          confirmVariant="primary"
          busy={rotate.isPending}
          error={
            rotate.isError ? proofingErrorMessage(rotate.error, t) : undefined
          }
          onConfirm={() =>
            rotate.mutate(undefined, {
              onSuccess: (hook) => {
                setConfirm(null);
                // An old test's failure was about the endpoint as it was.
                test.reset();
                if (hook.secret) onRotated(hook.secret);
              },
            })
          }
          onClose={() => {
            setConfirm(null);
            rotate.reset();
          }}
        />
      )}
      {confirm === "remove" && (
        <ConfirmDialog
          title={t("identityProofing.webhooks.removeConfirm.title")}
          message={t("identityProofing.webhooks.removeConfirm.message")}
          confirmLabel={t("identityProofing.webhooks.remove")}
          busy={remove.isPending}
          error={
            remove.isError ? proofingErrorMessage(remove.error, t) : undefined
          }
          onConfirm={() =>
            remove.mutate(undefined, { onSuccess: () => setConfirm(null) })
          }
          onClose={() => {
            setConfirm(null);
            remove.reset();
          }}
        />
      )}
    </Card>
  );
}

function DeliveriesCard({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const deliveries = useWebhookDeliveriesQuery(slug);
  const formatWhen = useWhenFormatter();
  const rows = ownEndpointDeliveries(deliveries.data ?? []);

  return (
    <Card>
      <h2 className="font-display border-line border-b px-5 py-4 text-[17px] font-bold">
        {t("identityProofing.webhooks.recent")}
      </h2>
      {deliveries.isError && (
        <p className={`${ERROR} px-5 py-4`}>
          {proofingErrorMessage(deliveries.error, t)}
        </p>
      )}
      {!deliveries.isError && rows.length === 0 && (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {deliveries.isPending
            ? t("common.loading")
            : t("identityProofing.webhooks.noDeliveries")}
        </p>
      )}
      {!deliveries.isError && rows.length > 0 && (
        <ul className="divide-line divide-y">
          {rows.map((d) => (
            <li
              key={d.id}
              className="flex flex-wrap items-center gap-x-4 gap-y-1.5 px-5 py-3 text-[12.5px]"
            >
              <span className="text-ink w-36 shrink-0 font-mono">
                {d.event}
              </span>
              <span className="text-ink-soft min-w-0 flex-1 basis-20 truncate font-mono">
                {d.sessionId ? shortRequestId(d.sessionId) : "—"}
              </span>
              <span className="shrink-0 whitespace-nowrap">
                <DeliveryStatus delivery={d} />
              </span>
              <span className="text-muted ml-auto min-w-20 shrink-0 text-right whitespace-nowrap">
                {formatWhen(d.lastAttemptAt ?? d.createdAt)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function DeliveryStatus({
  delivery,
}: {
  delivery: WebhookDelivery;
}): React.JSX.Element {
  const { t } = useTranslation();
  const code = delivery.lastStatusCode;
  if (delivery.status === "pending" && delivery.attempts === 0) {
    return <StatusText>{t("identityProofing.webhooks.queuedTag")}</StatusText>;
  }
  const label =
    code !== undefined ? String(code) : t("identityProofing.webhooks.noAnswer");
  const tone = code !== undefined && isSuccessStatus(code) ? "green" : "red";
  return (
    <span className="inline-flex items-center gap-2">
      <StatusText
        tone={tone}
        title={
          delivery.status === "pending"
            ? t("identityProofing.webhooks.retrying", {
                count: delivery.attempts,
              })
            : delivery.lastError
        }
      >
        {label}
      </StatusText>
      {delivery.status === "failed" && (
        <span className="text-error">
          {t("identityProofing.webhooks.gaveUp")}
        </span>
      )}
    </span>
  );
}
