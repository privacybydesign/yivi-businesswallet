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
  ProofingCustomer,
  ProofingWebhook,
  WebhookDelivery,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  isSuccessStatus,
  proofingErrorMessage,
  shortRequestId,
} from "../lib/identity-proofing";
import { Button, Card, ConfirmDialog, Input, Tag } from "../ui";
import { SecretReveal, WebhookStateText } from "./proofing-customer-ui";

const ERROR = "text-error text-[12.5px]";
const HINT = "text-muted text-[12px]";
const CAPTION =
  "text-muted font-mono text-[10.5px] font-medium tracking-[0.08em] uppercase";
// The masked middle of a secret whose last four characters are shown.
const SECRET_MASK = "••••••••••••••••";

// Where the customer's session results go: the wallet's own endpoint by
// default, or one the customer hosts, with the deliveries made to it.
export function WebhooksTab({
  slug,
  customer,
}: {
  slug: string;
  customer: ProofingCustomer;
}): React.JSX.Element {
  const { t } = useTranslation();
  const webhook = useProofingWebhookQuery(slug, customer.id);
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
      {!hook.configured && !editing ? (
        <DefaultEndpointCard onCustomize={() => setEditing(true)} />
      ) : editing ? (
        <EndpointForm
          slug={slug}
          customerId={customer.id}
          webhook={hook}
          onCancel={() => setEditing(false)}
          onSaved={(saved) => {
            setEditing(false);
            if (saved.secret) {
              setSecret(saved.secret);
            }
          }}
        />
      ) : (
        <EndpointCard
          slug={slug}
          customerId={customer.id}
          webhook={hook}
          onEdit={() => setEditing(true)}
          onRotated={setSecret}
        />
      )}
      <DeliveriesCard slug={slug} customerId={customer.id} />
      {secret && (
        <SecretReveal
          title={t("customers.webhooks.secretTitle")}
          hint={t("customers.webhooks.secretHint")}
          secret={secret}
          onClose={() => setSecret(null)}
        />
      )}
    </div>
  );
}

function EndpointForm({
  slug,
  customerId,
  webhook,
  onCancel,
  onSaved,
}: {
  slug: string;
  customerId: string;
  webhook: ProofingWebhook;
  onCancel?: () => void;
  onSaved: (saved: ProofingWebhook) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const save = useSaveWebhookMutation(slug, customerId);
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
            {t("customers.webhooks.endpoint")}
          </h2>
          <p className={`${HINT} mt-1`}>{t("customers.webhooks.formHint")}</p>
        </div>
        <div className="flex flex-col gap-4 px-5 py-4">
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="webhook-url"
              className="text-ink text-[13px] font-semibold"
            >
              {t("customers.webhooks.url")}
            </label>
            <Input
              id="webhook-url"
              type="url"
              value={url}
              className="font-mono"
              placeholder={t("customers.webhooks.urlPlaceholder")}
              onChange={(e) => setUrl(e.target.value)}
            />
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-ink mb-1 text-[13px] font-semibold">
              {t("customers.webhooks.events")}
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
              {t("customers.webhooks.save")}
            </Button>
            {onCancel && (
              <Button type="button" variant="secondary" onClick={onCancel}>
                {t("customers.new.cancel")}
              </Button>
            )}
          </div>
        </div>
      </form>
    </Card>
  );
}

// Without its own endpoint a customer's results go to the wallet's endpoint,
// which gets every session change; they show in Sessions and the audit log.
function DefaultEndpointCard({
  onCustomize,
}: {
  onCustomize: () => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Card>
      <div className="border-line flex items-center justify-between gap-3 border-b px-5 py-4">
        <h2 className="font-display text-[17px] font-bold">
          {t("customers.webhooks.endpoint")}
        </h2>
        <Tag tone="green" dot>
          {t("customers.webhooks.defaultTag")}
        </Tag>
      </div>
      <div className="flex flex-col gap-3 px-5 py-4">
        <p className="text-ink text-[13px]">
          {t("customers.webhooks.defaultBody")}
        </p>
        <p className={HINT}>{t("customers.webhooks.defaultHint")}</p>
        <div>
          <Button size="sm" variant="secondary" onClick={onCustomize}>
            {t("customers.webhooks.useOwn")}
          </Button>
        </div>
      </div>
    </Card>
  );
}

function EndpointCard({
  slug,
  customerId,
  webhook,
  onEdit,
  onRotated,
}: {
  slug: string;
  customerId: string;
  webhook: ProofingWebhook;
  onEdit: () => void;
  onRotated: (secret: string) => void;
}): React.JSX.Element {
  const { t } = useTranslation();
  const test = useSendWebhookTestMutation(slug, customerId);
  const rotate = useNewWebhookSecretMutation(slug, customerId);
  const remove = useRemoveWebhookMutation(slug, customerId);
  const [confirm, setConfirm] = useState<"rotate" | "remove" | null>(null);
  const failing = webhook.health.state === "failing";
  const error = test.error ?? rotate.error ?? remove.error;

  return (
    <Card>
      <div className="border-line flex items-center justify-between gap-3 border-b px-5 py-4">
        <h2 className="font-display text-[17px] font-bold">
          {t("customers.webhooks.endpoint")}
        </h2>
        {failing ? (
          <Tag tone="red" dot>
            {t("customers.webhooks.failing")}
          </Tag>
        ) : (
          <Tag tone="green" dot>
            {t("customers.webhooks.delivering")}
          </Tag>
        )}
      </div>
      <dl className="divide-line divide-y px-5">
        <div className="py-3">
          <dt className={CAPTION}>{t("customers.webhooks.url")}</dt>
          <dd className="text-ink mt-1 font-mono text-[12.5px] break-all">
            {webhook.url}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("customers.webhooks.events")}</dt>
          <dd className="mt-1.5 flex flex-wrap gap-1.5">
            {webhook.events.map((name) => (
              <span
                key={name}
                className="bg-surface-3 text-ink-soft rounded-full px-2 py-0.5 font-mono text-[11.5px]"
              >
                {name}
              </span>
            ))}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("customers.webhooks.secret")}</dt>
          <dd className="text-ink mt-1 font-mono text-[12.5px]">
            {`whsec_${SECRET_MASK}${webhook.secretLast4 ?? ""}`}
          </dd>
        </div>
        <div className="py-3">
          <dt className={CAPTION}>{t("customers.webhooks.retries")}</dt>
          <dd className="text-ink mt-1 text-[13px]">
            {t("customers.webhooks.retriesValue", {
              count: webhook.maxAttempts,
            })}
          </dd>
          {failing && (
            <dd className="mt-1 text-[12.5px]">
              <WebhookStateText webhook={webhook.health} />
              {webhook.health.pendingRetries > 0 &&
                ` · ${t("customers.webhooks.queued", {
                  count: webhook.health.pendingRetries,
                })}`}
            </dd>
          )}
        </div>
      </dl>
      {error && (
        <p className={`${ERROR} px-5`}>{proofingErrorMessage(error, t)}</p>
      )}
      <div className="flex flex-wrap gap-2 px-5 pt-2 pb-4">
        <Button
          size="sm"
          variant="secondary"
          loading={test.isPending}
          onClick={() => test.mutate()}
        >
          {t("customers.webhooks.test")}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirm("rotate")}>
          {t("customers.webhooks.rotate")}
        </Button>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={onEdit}>
          {t("customers.webhooks.edit")}
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirm("remove")}>
          {t("customers.webhooks.remove")}
        </Button>
      </div>
      {confirm === "rotate" && (
        <ConfirmDialog
          title={t("customers.webhooks.rotateConfirm.title")}
          message={t("customers.webhooks.rotateConfirm.message")}
          confirmLabel={t("customers.webhooks.rotate")}
          confirmVariant="primary"
          busy={rotate.isPending}
          onConfirm={() =>
            rotate.mutate(undefined, {
              onSuccess: (hook) => {
                setConfirm(null);
                if (hook.secret) onRotated(hook.secret);
              },
            })
          }
          onClose={() => setConfirm(null)}
        />
      )}
      {confirm === "remove" && (
        <ConfirmDialog
          title={t("customers.webhooks.removeConfirm.title")}
          message={t("customers.webhooks.removeConfirm.message")}
          confirmLabel={t("customers.webhooks.remove")}
          busy={remove.isPending}
          onConfirm={() =>
            remove.mutate(undefined, { onSuccess: () => setConfirm(null) })
          }
          onClose={() => setConfirm(null)}
        />
      )}
    </Card>
  );
}

function DeliveriesCard({
  slug,
  customerId,
}: {
  slug: string;
  customerId: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const deliveries = useWebhookDeliveriesQuery(slug, customerId);
  const formatWhen = useWhenFormatter();
  const rows = deliveries.data ?? [];

  return (
    <Card>
      <h2 className="font-display border-line border-b px-5 py-4 text-[17px] font-bold">
        {t("customers.webhooks.recent")}
      </h2>
      {deliveries.isError ? (
        <p className={`${ERROR} px-5 py-4`}>
          {proofingErrorMessage(deliveries.error, t)}
        </p>
      ) : rows.length === 0 ? (
        <p className="text-ink-soft px-5 py-4 text-[13px]">
          {deliveries.isPending
            ? t("common.loading")
            : t("customers.webhooks.noDeliveries")}
        </p>
      ) : (
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
              {d.endpointUrl === undefined && (
                <span className="text-muted shrink-0 whitespace-nowrap">
                  {t("customers.webhooks.defaultTag")}
                </span>
              )}
              <span className="shrink-0 whitespace-nowrap">
                <DeliveryTag delivery={d} />
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

function DeliveryTag({
  delivery,
}: {
  delivery: WebhookDelivery;
}): React.JSX.Element {
  const { t } = useTranslation();
  const code = delivery.lastStatusCode;
  if (delivery.status === "pending" && delivery.attempts === 0) {
    return <Tag tone="blue">{t("customers.webhooks.queuedTag")}</Tag>;
  }
  const label =
    code !== undefined ? String(code) : t("customers.webhooks.noAnswer");
  const tone = code !== undefined && isSuccessStatus(code) ? "green" : "red";
  return (
    <span className="inline-flex items-center gap-2">
      <Tag
        tone={tone}
        title={
          delivery.status === "pending"
            ? t("customers.webhooks.retrying", { count: delivery.attempts })
            : delivery.lastError
        }
      >
        {label}
      </Tag>
      {delivery.status === "failed" && (
        <span className="text-error">{t("customers.webhooks.gaveUp")}</span>
      )}
    </span>
  );
}
