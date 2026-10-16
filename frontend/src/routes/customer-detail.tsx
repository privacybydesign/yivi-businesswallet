import { useState } from "react";
import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { ApiError } from "../api/http";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingCustomerQuery,
  useProofingStatsQuery,
  useUpdateCustomerMutation,
} from "../api/identity-proofing.queries";
import { useDateFormatter } from "../lib/format-when";
import { toast } from "../lib/toast";
import {
  proofingErrorMessage,
  proofingStatsBy,
  shortRequestId,
  sumProofingStats,
} from "../lib/identity-proofing";
import { Button, ConfirmDialog, TopBar } from "../ui";
import { ApiKeysTab } from "./customer-api-keys-tab";
import { BrandingTab } from "./customer-branding-tab";
import { SettingsTab } from "./customer-settings-tab";
import { WebhooksTab } from "./customer-webhooks-tab";
import {
  CustomerMark,
  CustomerStatusTag,
  SecretReveal,
} from "./proofing-customer-ui";
import { FlowsTab, SendModal } from "./customer-flows-tab";
import { SessionsTab } from "./customer-sessions-tab";

const ERROR = "text-error text-[12.5px]";
const HTTP_NOT_FOUND = 404;

type TabKey =
  | "flows"
  | "branding"
  | "apiKeys"
  | "webhooks"
  | "sessions"
  | "settings";
const DEFAULT_TAB: TabKey = "flows";

type Tab = { key: TabKey; labelKey: `customers.tabs.${TabKey}` };
const tab = (key: TabKey): Tab => ({ key, labelKey: `customers.tabs.${key}` });
// A member sends for the customer and follows its sessions; the rest is the
// admin's configuration of it.
const MEMBER_TABS: Tab[] = [tab("flows"), tab("sessions")];
const ADMIN_TABS: Tab[] = [
  tab("flows"),
  tab("branding"),
  tab("apiKeys"),
  tab("webhooks"),
  tab("sessions"),
  tab("settings"),
];

// One customer of the org, in tabs: the flows assigned to it (an admin assigns
// them here), the sessions sent for it, and, for an admin, its settings. Any
// member verifies a person for it from the header (an e-mail address and an
// optional name; the person needs no account) unless an admin paused it. An
// admin sees every session for the customer, a member the ones they sent.
export default function CustomerDetail(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug, customerId } = useParams();
  // Guaranteed by the ":orgSlug/…/:customerId" route this component mounts under.
  const slug = orgSlug!;
  const id = customerId!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const customer = useProofingCustomerQuery(slug, id);
  const stats = useProofingStatsQuery(slug);
  const formatDate = useDateFormatter();
  const [searchParams, setSearchParams] = useSearchParams();
  const [sending, setSending] = useState(false);
  // A sent link, shown once outside the send dialog so closing that dialog
  // cannot discard it.
  const [link, setLink] = useState<string>();
  const [confirmingPause, setConfirmingPause] = useState(false);
  const update = useUpdateCustomerMutation(slug, id);

  const tabs = isAdmin ? ADMIN_TABS : MEMBER_TABS;
  const requested = searchParams.get("tab");
  // A member landing on ?tab=settings (a shared link) gets the default tab.
  const activeTab: TabKey =
    tabs.find((tab) => tab.key === requested)?.key ?? DEFAULT_TAB;
  const setTab = (tab: TabKey): void =>
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (tab === DEFAULT_TAB) next.delete("tab");
        else next.set("tab", tab);
        return next;
      },
      { replace: true },
    );

  const rows = (stats.data?.rows ?? []).filter((r) => r.customerId === id);
  const sessions = sumProofingStats(rows).sessions;
  const notFound =
    customer.error instanceof ApiError &&
    customer.error.status === HTTP_NOT_FOUND;
  const paused = customer.data?.status === "paused";
  const noApiKey = customer.data?.hasApiKey === false;

  return (
    <>
      <TopBar
        title={customer.data?.name ?? t("customers.title")}
        leading={
          customer.data && <CustomerMark customer={customer.data} size="lg" />
        }
        badges={customer.data && <CustomerStatusTag customer={customer.data} />}
        subtitle={
          customer.data &&
          t("customers.detail.since", {
            id: shortRequestId(customer.data.id),
            date: formatDate(customer.data.createdAt),
            count: sessions,
          })
        }
        actions={
          customer.data && (
            <>
              {isAdmin && (
                <Button
                  variant="secondary"
                  loading={update.isPending}
                  onClick={() =>
                    paused
                      ? update.mutate(
                          { paused: false },
                          {
                            onError: (error) =>
                              toast.error(proofingErrorMessage(error, t)),
                          },
                        )
                      : setConfirmingPause(true)
                  }
                >
                  {paused
                    ? t("customers.detail.resume")
                    : t("customers.detail.pause")}
                </Button>
              )}
              <Button
                icon="email"
                disabled={paused || noApiKey}
                onClick={() => setSending(true)}
              >
                {t("customers.detail.verify")}
              </Button>
            </>
          )
        }
      />
      {customer.data && sending && (
        <SendModal
          slug={slug}
          customer={customer.data}
          isAdmin={isAdmin}
          onClose={() => setSending(false)}
          onLink={(url) => {
            setSending(false);
            setLink(url);
          }}
        />
      )}
      {link !== undefined && (
        <SecretReveal
          title={t("customers.send.linkTitle")}
          hint={t("customers.send.linkHint")}
          secret={link}
          doneLabel={t("customers.send.linkDone")}
          onClose={() => setLink(undefined)}
        />
      )}
      {customer.data && confirmingPause && (
        <ConfirmDialog
          title={t("customers.detail.pauseConfirm.title", {
            name: customer.data.name,
          })}
          message={t("customers.detail.pauseConfirm.message")}
          confirmLabel={t("customers.detail.pauseConfirm.confirm")}
          busy={update.isPending}
          error={
            update.isError ? proofingErrorMessage(update.error, t) : undefined
          }
          onConfirm={() =>
            update.mutate(
              { paused: true },
              { onSuccess: () => setConfirmingPause(false) },
            )
          }
          onClose={() => {
            update.reset();
            setConfirmingPause(false);
          }}
        />
      )}
      {customer.isPending ? (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">
          {t("common.loading")}
        </p>
      ) : customer.isError ? (
        <p className={`${ERROR} p-4 sm:p-8`}>
          {notFound
            ? t("customers.detail.notFound")
            : proofingErrorMessage(customer.error, t)}
        </p>
      ) : (
        <>
          <div
            role="tablist"
            aria-label={customer.data.name}
            className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8"
            onKeyDown={(e) => {
              if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
              e.preventDefault();
              const i = tabs.findIndex((tab) => tab.key === activeTab);
              const delta = e.key === "ArrowRight" ? 1 : -1;
              const nextTab = tabs[(i + delta + tabs.length) % tabs.length];
              setTab(nextTab.key);
              document.getElementById(`customer-tab-${nextTab.key}`)?.focus();
            }}
          >
            {tabs.map((tab) => {
              const active = activeTab === tab.key;
              return (
                <button
                  key={tab.key}
                  id={`customer-tab-${tab.key}`}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  aria-controls="customer-tabpanel"
                  tabIndex={active ? 0 : -1}
                  onClick={() => setTab(tab.key)}
                  className={[
                    "h-11 border-b-2 px-3.5 text-[13.5px] whitespace-nowrap transition-colors",
                    active
                      ? "border-primary text-ink font-semibold"
                      : "text-ink-soft hover:text-ink border-transparent font-medium",
                  ].join(" ")}
                >
                  {t(tab.labelKey)}
                </button>
              );
            })}
          </div>
          <div
            id="customer-tabpanel"
            role="tabpanel"
            aria-labelledby={`customer-tab-${activeTab}`}
            className="flex flex-col gap-4 p-4 sm:p-8"
          >
            {paused && (
              <p className="bg-warning-bg text-warning-fg rounded-yivi px-4 py-3 text-[13px]">
                {t("customers.detail.pausedNotice")}
              </p>
            )}
            {!paused && noApiKey && (
              <div className="bg-warning-bg text-warning-fg rounded-yivi flex flex-wrap items-center justify-between gap-3 px-4 py-3 text-[13px]">
                <span>
                  {isAdmin
                    ? t("customers.detail.noApiKeyNotice")
                    : t("customers.detail.noApiKeyNoticeMember")}
                </span>
                {isAdmin && activeTab !== "apiKeys" && (
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => setTab("apiKeys")}
                  >
                    {t("customers.detail.createApiKey")}
                  </Button>
                )}
              </div>
            )}
            {activeTab === "flows" && (
              <FlowsTab
                slug={slug}
                customer={customer.data}
                isAdmin={isAdmin}
                sessionsByFlow={proofingStatsBy(rows, (r) => r.flowId)}
              />
            )}
            {activeTab === "branding" && isAdmin && (
              <BrandingTab
                // Not keyed on updatedAt: any save (a pause, another card) bumps
                // it, and remounting would throw away an edit in progress. Each
                // form reseeds itself from its own save's answer.
                slug={slug}
                customer={customer.data}
              />
            )}
            {activeTab === "apiKeys" && isAdmin && (
              <ApiKeysTab slug={slug} customer={customer.data} />
            )}
            {activeTab === "webhooks" && isAdmin && (
              <WebhooksTab slug={slug} customer={customer.data} />
            )}
            {activeTab === "sessions" && (
              <SessionsTab
                slug={slug}
                customer={customer.data}
                isAdmin={isAdmin}
              />
            )}
            {activeTab === "settings" && isAdmin && (
              <SettingsTab slug={slug} customer={customer.data} />
            )}
          </div>
        </>
      )}
    </>
  );
}
