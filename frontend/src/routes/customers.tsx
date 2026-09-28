import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useProofingCustomersQuery,
  useProofingStatsQuery,
} from "../api/identity-proofing.queries";
import { useDateFormatter } from "../lib/format-when";
import { usePercentFormatter } from "../lib/format-percent";
import {
  noProofingSessions,
  proofingErrorMessage,
  proofingStatsBy,
  searchCustomers,
  shortRequestId,
  verifiedShare,
} from "../lib/identity-proofing";
import { Button, Card, Icon, Input, Table, TopBar } from "../ui";
import {
  CustomerMark,
  CustomerStatusTag,
  NewCustomerModal,
  WebhookStateText,
} from "./proofing-customer-ui";

const CUSTOMER_COLUMNS = 7;
const SEARCH_ICON_SIZE = 15;
const OPEN_ICON_SIZE = 18;

// The org's customers (no login of their own): each is a party the org verifies
// external people for, on the flows an admin assigned to it. Every member sees
// the list and opens a customer to send requests; only an admin adds customers.
export default function Customers(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";
  const [adding, setAdding] = useState(false);

  return (
    <>
      <TopBar
        title={t("customers.title")}
        subtitle={t("customers.subtitle")}
        actions={
          isAdmin && (
            <Button icon="add" onClick={() => setAdding(true)}>
              {t("customers.add")}
            </Button>
          )
        }
      />
      {adding && (
        <NewCustomerModal slug={slug} onClose={() => setAdding(false)} />
      )}
      <div className="flex flex-col gap-4 p-4 sm:p-8">
        <CustomersTable slug={slug} />
      </div>
    </>
  );
}

function CustomersTable({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const customers = useProofingCustomersQuery(slug);
  const stats = useProofingStatsQuery(slug);
  const formatDate = useDateFormatter();
  const formatPercent = usePercentFormatter();
  const [search, setSearch] = useState("");
  const perCustomer = proofingStatsBy(
    stats.data?.rows ?? [],
    (r) => r.customerId,
  );
  const shown = searchCustomers(customers.data ?? [], search);

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="relative w-full max-w-xs">
          <span className="text-muted pointer-events-none absolute top-1/2 left-3 -translate-y-1/2">
            <Icon name="search" size={SEARCH_ICON_SIZE} />
          </span>
          <Input
            type="search"
            value={search}
            className="pl-9"
            placeholder={t("customers.list.search")}
            aria-label={t("customers.list.search")}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        {customers.data && (
          <span className="text-muted text-[12.5px]">
            {t("customers.list.count", { count: customers.data.length })}
          </span>
        )}
      </div>
      <Card>
        <Table>
          <Table.Head>
            <Table.HeaderCell>{t("customers.list.name")}</Table.HeaderCell>
            <Table.HeaderCell>{t("customers.list.flows")}</Table.HeaderCell>
            <Table.HeaderCell>{t("customers.list.sessions")}</Table.HeaderCell>
            <Table.HeaderCell>{t("customers.list.verified")}</Table.HeaderCell>
            <Table.HeaderCell>{t("customers.list.webhook")}</Table.HeaderCell>
            <Table.HeaderCell>{t("customers.list.status")}</Table.HeaderCell>
            <Table.HeaderCell aria-hidden="true" />
          </Table.Head>
          <Table.Body>
            {customers.isPending ? (
              <Table.State colSpan={CUSTOMER_COLUMNS}>
                {t("common.loading")}
              </Table.State>
            ) : customers.isError ? (
              <Table.State colSpan={CUSTOMER_COLUMNS}>
                {proofingErrorMessage(customers.error, t)}
              </Table.State>
            ) : customers.data.length === 0 ? (
              <Table.State colSpan={CUSTOMER_COLUMNS}>
                {t("customers.list.empty")}
              </Table.State>
            ) : shown.length === 0 ? (
              <Table.State colSpan={CUSTOMER_COLUMNS}>
                {t("customers.list.noMatch")}
              </Table.State>
            ) : (
              shown.map((customer) => {
                const to = `/${slug}/identity-proofing/customers/${customer.id}`;
                const totals =
                  perCustomer.get(customer.id) ?? noProofingSessions();
                const share = verifiedShare(totals);
                return (
                  <Table.Row
                    key={customer.id}
                    className="hover:bg-surface-2 cursor-pointer transition-colors"
                    onClick={() => void navigate(to)}
                  >
                    <Table.Cell>
                      <div className="flex items-center gap-3">
                        <CustomerMark customer={customer} />
                        <div className="min-w-0">
                          <Link
                            to={to}
                            className="text-ink font-semibold hover:underline"
                            onClick={(event) => event.stopPropagation()}
                          >
                            {customer.name}
                          </Link>
                          <div
                            className="text-muted font-mono text-[11.5px]"
                            title={t("customers.list.added", {
                              date: formatDate(customer.createdAt),
                            })}
                          >
                            {shortRequestId(customer.id)}
                          </div>
                        </div>
                      </div>
                    </Table.Cell>
                    <Table.Cell>{customer.flowIds.length}</Table.Cell>
                    <Table.Cell>
                      {stats.data ? totals.sessions : "—"}
                    </Table.Cell>
                    <Table.Cell>
                      {share === undefined ? "—" : formatPercent(share)}
                    </Table.Cell>
                    <Table.Cell className="text-[12.5px]">
                      <WebhookStateText webhook={customer.webhook} />
                    </Table.Cell>
                    <Table.Cell>
                      <CustomerStatusTag customer={customer} />
                    </Table.Cell>
                    <Table.Cell className="text-muted w-10 text-right">
                      <Icon name="chevron_right" size={OPEN_ICON_SIZE} />
                    </Table.Cell>
                  </Table.Row>
                );
              })
            )}
          </Table.Body>
        </Table>
      </Card>
    </>
  );
}
