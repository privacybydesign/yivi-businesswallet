import { Link, useNavigate, useParams, useSearchParams } from "react-router";
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

const SORT_VALUES: readonly MemberSort[] = [
  "name",
  "email",
  "jobtitle",
  "role",
  "department",
  "status",
];

function readStatus(params: URLSearchParams): StatusFilter {
  const raw = params.get("status");
  return raw === "active" || raw === "invited" ? raw : "";
}

function readSort(params: URLSearchParams): MemberSort {
  const raw = params.get("sort") as MemberSort | null;
  return raw && SORT_VALUES.includes(raw) ? raw : "name";
}

export default function Members(): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;

  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  // Sort, filter, search, and page live in the URL so the view survives a
  // refresh and can be bookmarked or shared.
  const [searchParams, setSearchParams] = useSearchParams();
  const status = readStatus(searchParams);
  const sort = readSort(searchParams);
  const dir: SortDir = searchParams.get("dir") === "desc" ? "desc" : "asc";
  const page = Math.max(
    0,
    Number.parseInt(searchParams.get("page") ?? "", 10) || 0,
  );
  const q = searchParams.get("q")?.trim() ?? "";

  const [searchInput, setSearchInput] = React.useState(
    () => searchParams.get("q") ?? "",
  );
  const debouncedSearch = useDebouncedValue(
    searchInput.trim(),
    SEARCH_DEBOUNCE_MS,
  );

  // The debounced search term is pushed to the URL (history-replaced so typing
  // doesn't flood Back); the guard avoids rewriting the URL it just read.
  React.useEffect(() => {
    if (debouncedSearch === q) return;
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (debouncedSearch) next.set("q", debouncedSearch);
        else next.delete("q");
        next.delete("page");
        return next;
      },
      { replace: true },
    );
  }, [debouncedSearch, q, setSearchParams]);

  const updateParams = (mutate: (params: URLSearchParams) => void): void => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      mutate(next);
      return next;
    });
  };

  const members = useOrganizationMembersQuery(
    slug,
    {
      status: status || undefined,
      q: q || undefined,
      sort,
      dir,
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
    },
    isAdmin,
  );

  // Per-status counts for the subtitle breakdown, independent of the active
  // filter/search (which only narrow the toolbar's result count).
  const activeCount = useOrganizationMembersQuery(
    slug,
    { status: "active", limit: 1 },
    isAdmin,
  );
  const pendingCount = useOrganizationMembersQuery(
    slug,
    { status: "invited", limit: 1 },
    isAdmin,
  );
  const activeTotal = activeCount.data?.total;
  const pendingTotal = pendingCount.data?.total;

  const resend = useResendInvitationMutation(slug);
  const revoke = useRevokeInvitationMutation(slug);

  const entries = members.data?.entries ?? [];
  const total = members.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const filtered = status !== "" || q !== "";

  const toggleSort = (column: MemberSort): void => {
    updateParams((params) => {
      if (sort === column && dir === "asc") {
        params.set("sort", column);
        params.set("dir", "desc");
      } else if (sort === column && dir === "desc") {
        // Third click clears the sort, returning to the default order.
        params.delete("sort");
        params.delete("dir");
      } else {
        params.set("sort", column);
        params.set("dir", "asc");
      }
      params.delete("page");
    });
  };

  const setStatus = (value: StatusFilter): void => {
    updateParams((params) => {
      if (value) params.set("status", value);
      else params.delete("status");
      params.delete("page");
    });
  };

  const goToPage = (next: number): void => {
    updateParams((params) => params.set("page", String(next)));
  };

  const isModified =
    status !== "" || q !== "" || sort !== "name" || dir !== "asc" || page !== 0;

  const resetView = (): void => {
    setSearchInput("");
    updateParams((params) => {
      for (const key of ["status", "q", "sort", "dir", "page"]) {
        params.delete(key);
      }
    });
  };

  const sortDirOf = (column: MemberSort): SortDir | null =>
    sort === column ? dir : null;

  return (
    <>
      <TopBar
        title={t("members.title")}
        subtitle={
          org.isPending ||
          (isAdmin && (activeCount.isPending || pendingCount.isPending))
            ? t("common.loading")
            : activeTotal !== undefined && pendingTotal !== undefined
              ? t("members.summary", {
                  active: activeTotal,
                  pending: pendingTotal,
                })
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
                  onChange={(event) => setSearchInput(event.target.value)}
                  aria-label={t("members.search")}
                />
              </div>
              <div className="bg-surface-3 rounded-yivi inline-flex gap-1 p-[3px]">
                {STATUS_FILTERS.map((value) => (
                  <button
                    key={value || "all"}
                    type="button"
                    onClick={() => setStatus(value)}
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
              {isModified && (
                <Button variant="ghost" size="sm" onClick={resetView}>
                  {t("members.reset")}
                </Button>
              )}
              {members.data && (
                <span className="text-muted ml-auto shrink-0 text-[12px] whitespace-nowrap">
                  {t("members.results", { count: total })}
                </span>
              )}
            </div>

            <Table className="table-fixed">
              <Table.Head>
                <Table.HeaderCell
                  className="w-[26%]"
                  sortDir={sortDirOf("name")}
                  onSort={() => toggleSort("name")}
                >
                  {t("members.columns.member")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  className="w-[15%]"
                  sortDir={sortDirOf("jobtitle")}
                  onSort={() => toggleSort("jobtitle")}
                >
                  {t("common.jobTitle")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  className="w-[15%]"
                  sortDir={sortDirOf("department")}
                  onSort={() => toggleSort("department")}
                >
                  {t("common.department")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  className="w-[12%]"
                  sortDir={sortDirOf("status")}
                  onSort={() => toggleSort("status")}
                >
                  {t("members.columns.status")}
                </Table.HeaderCell>
                <Table.HeaderCell
                  className="w-[12%]"
                  sortDir={sortDirOf("role")}
                  onSort={() => toggleSort("role")}
                >
                  {t("common.role")}
                </Table.HeaderCell>
                <Table.HeaderCell className="w-[20%]" srOnly>
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
                                <span className="text-ink block truncate">
                                  {fullName(member)}
                                </span>
                              ) : (
                                <Link
                                  to={`/${slug}/members/${member.userId}`}
                                  className="text-ink block truncate"
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
                        <Table.Cell className="text-ink-soft truncate">
                          {member.jobTitle ?? t("members.unassigned")}
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft truncate">
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
                    onClick={() => goToPage(Math.max(0, page - 1))}
                  >
                    <Icon name="chevron_left" size={14} />
                    {t("members.pager.previous")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={page >= pages - 1}
                    onClick={() => goToPage(Math.min(pages - 1, page + 1))}
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
