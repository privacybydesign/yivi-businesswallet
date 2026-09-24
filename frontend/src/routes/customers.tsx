import { useState } from "react";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useOrganizationQuery } from "../api/organization.queries";
import {
  useCreateProofingCustomerMutation,
  useProofingCustomersQuery,
} from "../api/identity-proofing.queries";
import { useWhenFormatter } from "../lib/format-when";
import { proofingErrorMessage } from "../lib/identity-proofing";
import { Button, Card, Input, Table, Tag, TopBar } from "../ui";

const ERROR = "text-error text-[12.5px]";
const CUSTOMER_COLUMNS = 3;

// The org's customers (sub-tenants with no login of their own): each is the
// party the org verifies external people for, on the flows an admin assigned to
// it. Every member sees the list and opens a customer to send requests; only an
// admin adds customers.
export default function Customers(): React.JSX.Element {
  const { t } = useTranslation();
  const { orgSlug } = useParams();
  // Guaranteed by the ":orgSlug" route segment this component mounts under.
  const slug = orgSlug!;
  const org = useOrganizationQuery(slug);
  const isAdmin = org.data?.role === "admin";

  return (
    <>
      <TopBar title={t("customers.title")} subtitle={t("customers.subtitle")} />
      <div className="flex flex-col gap-6 p-8">
        {isAdmin && <NewCustomerCard slug={slug} />}
        <CustomersTable slug={slug} />
      </div>
    </>
  );
}

function NewCustomerCard({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const create = useCreateProofingCustomerMutation(slug);
  const [name, setName] = useState("");
  const [touched, setTouched] = useState(false);
  const missing = name.trim() === "";

  function submit(event: React.FormEvent): void {
    event.preventDefault();
    setTouched(true);
    if (missing) {
      return;
    }
    create.mutate(name.trim(), {
      onSuccess: () => {
        setName("");
        setTouched(false);
      },
    });
  }

  return (
    <Card className="p-6">
      <h2 className="font-display text-[16px] font-bold">
        {t("customers.new.title")}
      </h2>
      <form
        className="mt-4 flex flex-wrap items-start gap-3"
        onSubmit={submit}
        noValidate
      >
        <div className="flex w-full max-w-sm flex-col gap-1">
          <label
            htmlFor="proofing-customer-name"
            className="text-ink-soft text-[12px] font-semibold"
          >
            {t("customers.new.name")}
          </label>
          <Input
            id="proofing-customer-name"
            value={name}
            placeholder={t("customers.new.namePlaceholder")}
            aria-invalid={touched && missing}
            onChange={(event) => setName(event.target.value)}
          />
          {touched && missing && (
            <p className={ERROR}>{t("customers.new.nameRequired")}</p>
          )}
          {create.isError && (
            <p className={ERROR}>{proofingErrorMessage(create.error, t)}</p>
          )}
        </div>
        <Button
          type="submit"
          icon="add"
          className="sm:mt-[22px]"
          loading={create.isPending}
        >
          {t("customers.new.create")}
        </Button>
      </form>
    </Card>
  );
}

function CustomersTable({ slug }: { slug: string }): React.JSX.Element {
  const { t } = useTranslation();
  const customers = useProofingCustomersQuery(slug);
  const formatWhen = useWhenFormatter();

  return (
    <Card>
      <div className="px-6 pt-5 pb-3">
        <h2 className="font-display text-[16px] font-bold">
          {t("customers.list.title")}
        </h2>
        <p className="text-ink-soft mt-1 text-[12px]">
          {t("customers.list.hint")}
        </p>
      </div>
      <Table>
        <Table.Head>
          <Table.HeaderCell>{t("customers.list.name")}</Table.HeaderCell>
          <Table.HeaderCell>{t("customers.list.flows")}</Table.HeaderCell>
          <Table.HeaderCell>{t("customers.list.created")}</Table.HeaderCell>
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
          ) : (
            customers.data.map((customer) => (
              <Table.Row key={customer.id}>
                <Table.Cell>
                  <Link
                    to={`/${slug}/customers/${customer.id}`}
                    className="text-link font-semibold underline"
                    aria-label={t("customers.list.open", {
                      name: customer.name,
                    })}
                  >
                    {customer.name}
                  </Link>
                </Table.Cell>
                <Table.Cell>
                  {customer.flowIds.length === 0 ? (
                    <Tag tone="amber">{t("customers.list.noFlows")}</Tag>
                  ) : (
                    <Tag tone="blue">
                      {t("customers.list.flowCount", {
                        count: customer.flowIds.length,
                      })}
                    </Tag>
                  )}
                </Table.Cell>
                <Table.Cell>{formatWhen(customer.createdAt)}</Table.Cell>
              </Table.Row>
            ))
          )}
        </Table.Body>
      </Table>
    </Card>
  );
}
