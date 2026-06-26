import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { useOrganizationsQuery } from "../api/organization.queries";
import { Avatar, Button, Card, Input, TopBar } from "../ui";
import * as React from "react";

export default function AllOrganizations(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data, isPending, isError, error } = useOrganizationsQuery();
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
        title={t("allOrganizations.title")}
        subtitle={
          isPending
            ? t("common.loading")
            : t("allOrganizations.count", { count: data?.length ?? 0 })
        }
        actions={
          <>
            <div className="w-64">
              <Input
                icon="search"
                placeholder={t("allOrganizations.searchPlaceholder")}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                aria-label={t("allOrganizations.searchPlaceholder")}
              />
            </div>
            <Button
              icon="add"
              onClick={() => void navigate("/admin/organizations/new")}
            >
              {t("allOrganizations.create")}
            </Button>
          </>
        }
      />

      <div className="p-8">
        {isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("allOrganizations.loadError", { message: error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <table className="w-full border-collapse text-[13.5px]">
              <thead>
                <tr>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("allOrganizations.columnOrganization")}
                  </th>
                  <th className="border-line bg-surface-2 text-muted border-b px-3.5 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.06em] uppercase">
                    {t("common.slug")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {isPending ? (
                  <tr>
                    <td className="text-ink-soft px-3.5 py-3" colSpan={2}>
                      {t("common.loading")}
                    </td>
                  </tr>
                ) : filtered.length === 0 ? (
                  <tr>
                    <td className="text-ink-soft px-3.5 py-3" colSpan={2}>
                      {data && data.length > 0
                        ? t("allOrganizations.noMatch")
                        : t("allOrganizations.none")}
                    </td>
                  </tr>
                ) : (
                  filtered.map((org) => (
                    <tr
                      key={org.id}
                      className="hover:bg-surface-3 transition-colors"
                    >
                      <td className="border-line border-b px-3.5 py-3">
                        <Link
                          to={`/${org.slug}`}
                          className="flex items-center gap-2.5"
                        >
                          <Avatar name={org.name} tone="rose" />
                          <span className="text-ink font-semibold">
                            {org.name}
                          </span>
                        </Link>
                      </td>
                      <td className="border-line text-ink-soft border-b px-3.5 py-3 font-mono text-[12px]">
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
