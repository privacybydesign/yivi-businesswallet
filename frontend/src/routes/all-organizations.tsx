import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useDeleteOrganizationMutation,
  useOrganizationsQuery,
} from "../api/organization.queries";
import {
  useProofingPausesQuery,
  useSetPlatformProofingPauseMutation,
} from "../api/identity-proofing.queries";
import type { ProofingPause } from "../api/identity-proofing";
import { Avatar, Button, Card, Input, Table, Tag, TopBar } from "../ui";
import * as React from "react";

const COLUMNS = 5;

export default function AllOrganizations(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { data, isPending, isError, error } = useOrganizationsQuery();
  const del = useDeleteOrganizationMutation();
  const pauses = useProofingPausesQuery();
  const pauseOf = useMemo(
    () => new Map((pauses.data ?? []).map((p) => [p.organizationId, p])),
    [pauses.data],
  );
  const [query, setQuery] = useState("");

  function handleDelete(
    event: React.MouseEvent,
    org: { id: string; name: string },
  ): void {
    event.stopPropagation();
    if (
      window.confirm(t("allOrganizations.confirmDelete", { name: org.name }))
    ) {
      del.mutate(org.id);
    }
  }

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
          <div className="w-64">
            <Input
              icon="search"
              placeholder={t("allOrganizations.searchPlaceholder")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              aria-label={t("allOrganizations.searchPlaceholder")}
            />
          </div>
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
            <Table>
              <Table.Head>
                <Table.HeaderCell>
                  {t("allOrganizations.columnOrganization")}
                </Table.HeaderCell>
                <Table.HeaderCell>
                  {t("allOrganizations.columnKvk")}
                </Table.HeaderCell>
                <Table.HeaderCell>{t("common.slug")}</Table.HeaderCell>
                <Table.HeaderCell>
                  {t("identityProofing.pause.platformColumn")}
                </Table.HeaderCell>
                <Table.HeaderCell className="text-right">
                  {t("allOrganizations.columnActions")}
                </Table.HeaderCell>
              </Table.Head>
              <Table.Body>
                {isPending ? (
                  <Table.State colSpan={COLUMNS}>
                    {t("common.loading")}
                  </Table.State>
                ) : filtered.length === 0 ? (
                  <Table.State colSpan={COLUMNS}>
                    {data && data.length > 0
                      ? t("allOrganizations.noMatch")
                      : t("allOrganizations.none")}
                  </Table.State>
                ) : (
                  filtered.map((org) => (
                    <Table.Row
                      key={org.id}
                      onClick={() => void navigate(`/${org.slug}`)}
                      className="hover:bg-surface-3 cursor-pointer transition-colors"
                    >
                      <Table.Cell>
                        <Link
                          to={`/${org.slug}`}
                          className="flex items-center gap-2.5"
                        >
                          <Avatar
                            name={org.name}
                            src={org.logoUri}
                            alt={org.name}
                            tone="rose"
                          />
                          <span className="text-ink font-semibold">
                            {org.name}
                          </span>
                        </Link>
                      </Table.Cell>
                      <Table.Cell className="text-ink-soft font-mono text-[12px]">
                        {org.kvkNumber ?? "—"}
                      </Table.Cell>
                      <Table.Cell className="text-ink-soft font-mono text-[12px]">
                        {org.slug}
                      </Table.Cell>
                      <Table.Cell>
                        <ProofingPauseCell
                          orgId={org.id}
                          pause={pauseOf.get(org.id)}
                        />
                      </Table.Cell>
                      <Table.Cell className="text-right">
                        <Button
                          variant="dangerGhost"
                          icon="delete"
                          iconOnly
                          aria-label={t("allOrganizations.delete")}
                          disabled={del.isPending}
                          onClick={(event) => handleDelete(event, org)}
                        />
                      </Table.Cell>
                    </Table.Row>
                  ))
                )}
              </Table.Body>
            </Table>
          </Card>
        )}
      </div>
    </>
  );
}

// An org's identity proofing as the platform admin sees it, with their pause.
// The org's own switch shows but is theirs to lift.
function ProofingPauseCell({
  orgId,
  pause,
}: {
  orgId: string;
  pause: ProofingPause | undefined;
}): React.JSX.Element {
  const { t } = useTranslation();
  const set = useSetPlatformProofingPauseMutation();
  const byPlatform = pause?.platformPausedAt !== undefined;
  const byOrganization = pause?.orgPausedAt !== undefined;

  return (
    <div
      className="flex flex-wrap items-center gap-2"
      // The row opens the org; this cell's button must not.
      onClick={(event) => event.stopPropagation()}
    >
      {byPlatform ? (
        <Tag tone="amber">{t("identityProofing.pause.pausedByPlatform")}</Tag>
      ) : byOrganization ? (
        <Tag tone="default">
          {t("identityProofing.pause.pausedByOrganization")}
        </Tag>
      ) : (
        <Tag tone="green">{t("identityProofing.pause.active")}</Tag>
      )}
      <Button
        size="sm"
        variant="secondary"
        loading={set.isPending}
        onClick={() => set.mutate({ orgId, paused: !byPlatform })}
      >
        {byPlatform
          ? t("identityProofing.pause.platformResume")
          : t("identityProofing.pause.platformPause")}
      </Button>
    </div>
  );
}
