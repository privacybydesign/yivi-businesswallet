import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { useMyOrganizationsQuery } from "../api/organization.queries";
import { Avatar, Card, Input, TopBar } from "../ui";
import * as React from "react";

export default function Organizations(): React.JSX.Element {
  const { t } = useTranslation();
  const { data, isPending, isError, error } = useMyOrganizationsQuery();
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => {
    if (!data) {
      return [];
    }
    const needle = query.trim().toLowerCase();
    if (needle === "") {
      return data;
    }
    return data.filter((org) => org.name.toLowerCase().includes(needle));
  }, [data, query]);

  return (
    <>
      <TopBar
        title={t("organizations.title")}
        subtitle={
          isPending
            ? t("common.loading")
            : t("organizations.count", { count: data?.length ?? 0 })
        }
        actions={
          <div className="w-64">
            <Input
              icon="search"
              placeholder={t("organizations.searchPlaceholder")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              aria-label={t("organizations.searchPlaceholder")}
            />
          </div>
        }
      />

      <div className="p-8">
        {isError ? (
          <Card className="p-6">
            <p className="text-[14px] text-error">
              {t("organizations.loadError", { message: error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <table className="w-full border-collapse text-[13.5px]">
              <thead>
                <tr>
                  <th className="border-b border-line bg-surface-2 px-3.5 py-2.5 text-left font-mono text-[11px] font-medium uppercase tracking-[0.06em] text-muted">
                    {t("organizations.columnOrganization")}
                  </th>
                  <th className="border-b border-line bg-surface-2 px-3.5 py-2.5 text-left font-mono text-[11px] font-medium uppercase tracking-[0.06em] text-muted">
                    {t("common.slug")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {isPending ? (
                  <tr>
                    <td className="px-3.5 py-3 text-ink-soft" colSpan={2}>
                      {t("common.loading")}
                    </td>
                  </tr>
                ) : filtered.length === 0 ? (
                  <tr>
                    <td className="px-3.5 py-3 text-ink-soft" colSpan={2}>
                      {data && data.length > 0
                        ? t("organizations.noMatch")
                        : t("organizations.none")}
                    </td>
                  </tr>
                ) : (
                  filtered.map((org) => (
                    <tr
                      key={org.id}
                      className="transition-colors hover:bg-surface-3"
                    >
                      <td className="border-b border-line px-3.5 py-3">
                        <Link
                          to={`/${org.slug}`}
                          className="flex items-center gap-2.5"
                        >
                          <Avatar name={org.name} tone="rose" />
                          <span className="font-semibold text-ink">
                            {org.name}
                          </span>
                        </Link>
                      </td>
                      <td className="border-b border-line px-3.5 py-3 font-mono text-[12px] text-ink-soft">
                        {org.slug}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </Card>
        )}
      </div>
    </>
  );
}
