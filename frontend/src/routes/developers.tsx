import { useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import { absoluteApiUrl } from "../api/http";
import { useProofingSettingsQuery } from "../api/identity-proofing.queries";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { TopBar } from "../ui";
import { ApiTokensSection } from "./developers-api-keys";
import { WebhookSection } from "./developers-webhook";
import { SettingsTab } from "./proofing-settings";

type TabKey = "api" | "settings";
const DEFAULT_TAB: TabKey = "api";
// The API reference the backend serves (internal/apidocs).
const API_DOCS_PATH = "/api/docs";

const TABS: {
  key: TabKey;
  labelKey: `identityProofing.developers.tabs.${TabKey}`;
}[] = [
  { key: "api", labelKey: "identityProofing.developers.tabs.api" },
  { key: "settings", labelKey: "identityProofing.developers.tabs.settings" },
];

// What an org sets up once for identity proofing, for all its flows: the API
// tokens and the webhook endpoint its own software uses, and, until they get
// their own page, the session settings. Admin only.
export default function Developers(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const settings = useProofingSettingsQuery(slug);
  const [searchParams, setSearchParams] = useSearchParams();

  const requested = searchParams.get("tab");
  const activeTab: TabKey =
    TABS.find((tab) => tab.key === requested)?.key ?? DEFAULT_TAB;
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

  // What shows instead of the tabs to anyone but an admin, and until the org
  // is known.
  const notice = org.isPending
    ? t("common.loading")
    : t("identityProofing.developers.adminOnly");
  // On the focused tab, as the tablist itself takes no focus.
  function onTabKeyDown(e: React.KeyboardEvent): void {
    if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
    e.preventDefault();
    const i = TABS.findIndex((tab) => tab.key === activeTab);
    const delta = e.key === "ArrowRight" ? 1 : -1;
    const next = TABS[(i + delta + TABS.length) % TABS.length];
    setTab(next.key);
    document.getElementById(`developers-tab-${next.key}`)?.focus();
  }

  return (
    <>
      <TopBar
        title={t("identityProofing.developers.title")}
        subtitle={t("identityProofing.developers.subtitle")}
        actions={
          <a
            href={absoluteApiUrl(API_DOCS_PATH)}
            target="_blank"
            rel="noreferrer"
            className="text-link inline-flex h-9 items-center px-1 text-[13.5px] font-semibold underline"
          >
            {t("identityProofing.overview.apiDocs")}
          </a>
        }
      />
      {isAdmin ? (
        <>
          <div
            role="tablist"
            aria-label={t("identityProofing.developers.title")}
            className="border-line bg-surface flex gap-1 overflow-x-auto border-b px-4 sm:px-8"
          >
            {TABS.map((tab) => {
              const active = activeTab === tab.key;
              return (
                <button
                  key={tab.key}
                  id={`developers-tab-${tab.key}`}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  aria-controls="developers-tabpanel"
                  tabIndex={active ? 0 : -1}
                  onClick={() => setTab(tab.key)}
                  onKeyDown={onTabKeyDown}
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
            id="developers-tabpanel"
            role="tabpanel"
            aria-labelledby={`developers-tab-${activeTab}`}
            className="flex flex-col gap-6 p-4 sm:p-8"
          >
            {activeTab === "api" && (
              <>
                <ApiTokensSection slug={slug} />
                <WebhookSection slug={slug} />
              </>
            )}
            {activeTab === "settings" && settings.isPending && (
              <p className="text-ink-soft text-[14px]">{t("common.loading")}</p>
            )}
            {activeTab === "settings" && settings.isError && (
              <p className="text-error text-[12.5px]">
                {proofingErrorMessage(settings.error, t)}
              </p>
            )}
            {activeTab === "settings" && settings.isSuccess && (
              <SettingsTab slug={slug} settings={settings.data} />
            )}
          </div>
        </>
      ) : (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">{notice}</p>
      )}
    </>
  );
}
