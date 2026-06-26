import { useEffect, useRef, useState } from "react";
import { useMatches, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import type { Organization } from "../api/organization";
import { Avatar } from "./avatar";
import { Icon } from "./icon";
import * as React from "react";

const CHEVRON_SIZE = 14;
const GLYPH_SIZE = 14;
const ALL_ORGS_PATH = "/admin/organizations";

interface OrgSwitcherProps {
  organizations: Organization[];
  isPending: boolean;
  isPlatformAdmin: boolean;
}

export function OrgSwitcher({
  organizations,
  isPending,
  isPlatformAdmin,
}: OrgSwitcherProps): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const matches = useMatches();
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const activeSlug = matches.find(
    (match) => (match.params as { orgSlug?: string }).orgSlug !== undefined,
  )?.params.orgSlug;
  const activeOrg = organizations.find((org) => org.slug === activeSlug);

  useEffect(() => {
    if (!open) {
      return;
    }
    function handlePointerDown(event: MouseEvent): void {
      if (!containerRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  function go(path: string): void {
    setOpen(false);
    void navigate(path);
  }

  const triggerLabel = activeOrg?.name ?? t("orgSwitcher.allOrganizations");

  return (
    <div ref={containerRef} className="relative px-2.5 pt-3 pb-1.5">
      <button
        type="button"
        onClick={() => setOpen((prev) => !prev)}
        disabled={isPending}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("orgSwitcher.label")}
        className="bg-surface-3 hover:bg-line flex w-full items-center gap-2.5 rounded-md px-3 py-2.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60"
      >
        {activeOrg ? (
          <Avatar name={activeOrg.name} tone="rose" />
        ) : (
          <span className="bg-line text-ink-soft flex h-7 w-7 shrink-0 items-center justify-center rounded-full">
            <Icon name="personal" size={GLYPH_SIZE} />
          </span>
        )}
        <span className="min-w-0 flex-1">
          <span className="font-display text-ink block truncate text-[13px] font-semibold">
            {triggerLabel}
          </span>
          {activeOrg && (
            <span className="text-ink-soft block truncate font-mono text-[10.5px]">
              {activeOrg.slug}
            </span>
          )}
        </span>
        <Icon
          name={open ? "chevron_up" : "chevron_down"}
          size={CHEVRON_SIZE}
          className="text-ink-soft"
        />
      </button>

      {open && (
        <div
          role="menu"
          className="border-line bg-surface shadow-card absolute inset-x-2.5 top-full z-20 mt-1 overflow-hidden rounded-md border"
        >
          {organizations.length === 0 ? (
            <p className="text-ink-soft px-3 py-2.5 text-[12.5px]">
              {t("orgSwitcher.empty")}
            </p>
          ) : (
            <ul className="max-h-64 overflow-y-auto py-1">
              {organizations.map((org) => {
                const isActive = org.slug === activeSlug;
                return (
                  <li key={org.id}>
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => go(`/${org.slug}`)}
                      className={[
                        "hover:bg-surface-3 flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors",
                        isActive ? "bg-surface-3" : "",
                      ].join(" ")}
                    >
                      <Avatar name={org.name} tone="rose" />
                      <span className="min-w-0 flex-1">
                        <span className="text-ink block truncate text-[13px] font-semibold">
                          {org.name}
                        </span>
                        <span className="text-ink-soft block truncate font-mono text-[10.5px]">
                          {org.slug}
                        </span>
                      </span>
                      {isActive && (
                        <Icon
                          name="valid"
                          size={GLYPH_SIZE}
                          className="text-primary"
                        />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}

          {isPlatformAdmin && (
            <button
              type="button"
              role="menuitem"
              onClick={() => go(ALL_ORGS_PATH)}
              className="border-line text-ink-soft hover:bg-surface-3 hover:text-ink flex w-full items-center gap-2 border-t px-3 py-2.5 text-left text-[12.5px] font-medium transition-colors"
            >
              <Icon name="settings" size={GLYPH_SIZE} />
              {t("orgSwitcher.manageAll")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
