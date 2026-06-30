import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  useOrganizationMembersQuery,
  useOrganizationQuery,
  useResendInvitationMutation,
  useRevokeInvitationMutation,
} from "../api/organization.queries";
import type { MemberSort } from "../api/organization";
import { accessMessage } from "../lib/access-message";
import { fullName, personInitials } from "../lib/name";
import { useDebouncedValue } from "../lib/use-debounced-value";
import { Avatar, Button, Card, Icon, Input, Table, Tag, TopBar } from "../ui";
import type { SortDir } from "../ui";
import * as React from "react";

const PAGE_SIZE = 25;
const SEARCH_DEBOUNCE_MS = 300;
const COLUMN_COUNT = 6;

type StatusFilter = "" | "active" | "invited";

const STATUS_FILTERS: readonly StatusFilter[] = ["", "active", "invited"];

export default function Members(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  const [status, setStatus] = React.useState<StatusFilter>("");
  const [searchInput, setSearchInput] = React.useState("");
  const [sort, setSort] = React.useState<MemberSort>("name");
  const [dir, setDir] = React.useState<SortDir>("asc");
  const [page, setPage] = React.useState(0);

  const search = useDebouncedValue(searchInput.trim(), SEARCH_DEBOUNCE_MS);

  const members = useOrganizationMembersQuery(
    slug,
    {
      status: status || undefined,
      q: search || undefined,
      sort,
      dir,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    },
    isAdmin,
  );

  const resend = useResendInvitationMutation(slug);
  const revoke = useRevokeInvitationMutation(slug);

  const entries = members.data?.entries ?? [];
  const total = members.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const filtered = status !== "" || search !== "";

  const toggleSort = (column: MemberSort): void => {
    if (sort === column) {
      setDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSort(column);
      setDir("asc");
    }
    setPage(0);
  };

  const sortDirOf = (column: MemberSort): SortDir | null =>
    sort === column ? dir : null;

  return (
    <>
      <TopBar
        title={t("members.title")}
        subtitle={
          org.isPending || (isAdmin && members.isPending)
            ? t("common.loading")
            : members.data
              ? t("members.count", { count: total })
              : t("members.subtitle")
        }
        actions={
          isAdmin ? (
            <Button
              icon="add"
              onClick={() => void navigate(`/${slug}/members/invite`)}
            >
              {t("members.invite")}
            </Button>
          ) : undefined
        }
      />

      <div className="p-8">
        {org.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {accessMessage(org.error, t)}
            </p>
          </Card>
        ) : !org.isPending && !isAdmin ? (
          <Card className="p-6">
            <p className="text-ink-soft text-[14px]">
              {t("members.adminOnly")}
            </p>
          </Card>
        ) : members.isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("members.loadError", { message: members.error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <div className="border-line flex items-center gap-3 border-b px-4 py-3">
              <div className="max-w-[320px] flex-1">
                <Input
                  icon="search"
                  placeholder={t("members.search")}
                  value={searchInput}
                  onChange={(event) => {
                    setSearchInput(event.target.value);
                    setPage(0);
                  }}
                  aria-label={t("members.search")}
                />
              </div>
              <div className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]">
                {STATUS_FILTERS.map((value) => (
                  <button
                    key={value || "all"}
                    type="button"
                    onClick={() => {
                      setStatus(value);
                      setPage(0);
                    }}
                    className={[
                      "h-[26px] cursor-pointer rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
                      status === value
                        ? "bg-surface text-ink shadow-sm"
                        : "text-ink-soft hover:text-ink",
                    ].join(" ")}
                  >
                    {value === ""
                      ? t("members.filters.all")
                      : value === "active"
                        ? t("members.filters.active")
                        : t("members.filters.pending")}
                  </button>
                ))}
              </div>
            </div>

            <Table>
              <Table.Head>
                <Table.HeaderCell
                  sortDir={sortDirOf("name")}
                  onSort={() => toggleSort("name")}
                >
                  {t("members.columns.member")}
                </Table.HeaderCell>
                <Table.HeaderCell>{t("common.jobTitle")}</Table.HeaderCell>
                <Table.HeaderCell
                  sortDir={sortDirOf("department")}
                  onSort={() => toggleSort("department")}
                >
                  {t("common.department")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  sortDir={sortDirOf("status")}
                  onSort={() => toggleSort("status")}
                >
                  {t("members.columns.status")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  sortDir={sortDirOf("role")}
                  onSort={() => toggleSort("role")}
                >
                  {t("common.role")}
                </Table.HeaderCell>
                <Table.HeaderCell srOnly>
                  {t("members.columns.actions")}
                </Table.HeaderCell>
              </Table.Head>
              <Table.Body>
                {org.isPending || members.isPending ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {t("common.loading")}
                  </Table.State>
                ) : entries.length === 0 ? (
                  <Table.State colSpan={COLUMN_COUNT}>
                    {filtered ? t("members.noMatch") : t("members.empty")}
                  </Table.State>
                ) : (
                  entries.map((member) => {
                    const pending = member.status === "invited";
                    return (
                      <Table.Row
                        key={
                          member.userId ?? member.invitationId ?? member.email
                        }
                        onClick={
                          pending
                            ? undefined
                            : () =>
                                void navigate(
                                  `/${slug}/members/${member.userId}`,
                                )
                        }
                        className={
                          pending
                            ? ""
                            : "hover:bg-surface-3 cursor-pointer transition-colors"
                        }
                      >
                        <Table.Cell>
                          <div className="flex items-center gap-2.5">
                            <Avatar initials={personInitials(member)} />
                            <div className="min-w-0">
                              {pending ? (
                                <span className="text-ink truncate">
                                  {fullName(member)}
                                </span>
                              ) : (
                                <Link
                                  to={`/${slug}/members/${member.userId}`}
                                  className="text-ink truncate"
                                >
                                  {fullName(member)}
                                </Link>
                              )}
                              <div className="text-ink-soft truncate text-[12px]">
                                {member.email}
                              </div>
                            </div>
                          </div>
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft">
                          {member.jobTitle ?? t("members.unassigned")}
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft">
                          {member.departmentName ?? t("members.unassigned")}
                        </Table.Cell>
                        <Table.Cell>
                          {pending ? (
                            <Tag tone="amber" dot>
                              {t("members.pending")}
                            </Tag>
                          ) : (
                            <Tag tone="green" dot>
                              {t("members.active")}
                            </Tag>
                          )}
                        </Table.Cell>
                        <Table.Cell>
                          <Tag
                            tone={member.role === "admin" ? "blue" : "default"}
                          >
                            <span className="capitalize">{member.role}</span>
                          </Tag>
                        </Table.Cell>
                        <Table.Cell className="text-right">
                          {pending && member.invitationId && (
                            <div className="flex items-center justify-end gap-2">
                              <Button
                                size="sm"
                                variant="ghost"
                                icon="email"
                                disabled={resend.isPending}
                                onClick={(event) => {
                                  event.stopPropagation();
                                  resend.mutate({
                                    invitationId: member.invitationId!,
                                  });
                                }}
                              >
                                {t("members.resend")}
                              </Button>
                              <Button
                                size="sm"
                                variant="danger"
                                icon="delete"
                                disabled={revoke.isPending}
                                onClick={(event) => {
                                  event.stopPropagation();
                                  revoke.mutate({
                                    invitationId: member.invitationId!,
                                  });
                                }}
                              >
                                {t("members.revoke")}
                              </Button>
                            </div>
                          )}
                        </Table.Cell>
                      </Table.Row>
                    );
                  })
                )}
              </Table.Body>
            </Table>

            {pages > 1 && (
              <div className="border-line bg-surface-2 flex items-center justify-between border-t px-4 py-2.5">
                <span className="text-muted text-[12px]">
                  {t("members.pager.page", { page: page + 1, pages })}
                </span>
                <div className="flex gap-1">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={page === 0}
                    onClick={() => setPage((p) => Math.max(0, p - 1))}
                  >
                    <Icon name="chevron_left" size={14} />
                    {t("members.pager.previous")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={page >= pages - 1}
                    onClick={() => setPage((p) => Math.min(pages - 1, p + 1))}
                  >
                    {t("members.pager.next")}
                    <Icon name="chevron_right" size={14} />
                  </Button>
                </div>
              </div>
            )}
          </Card>
        )}
      </div>
    </>
  );
}
