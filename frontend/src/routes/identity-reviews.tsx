import { useTranslation } from "react-i18next";
import {
  useIdentityReviewsQuery,
  useResolveIdentityReviewMutation,
} from "../api/invitations.queries";
import { toast } from "../lib/toast";
import { Button, Card, Icon, Table, TopBar } from "../ui";
import * as React from "react";

export default function IdentityReviews(): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { data, isPending, isError, error } = useIdentityReviewsQuery();
  const resolve = useResolveIdentityReviewMutation();

  const dateFormatter = React.useMemo(
    () =>
      new Intl.DateTimeFormat(i18n.language, {
        dateStyle: "medium",
        timeStyle: "short",
      }),
    [i18n.language],
  );

  const onResolve = (id: string, approve: boolean): void => {
    resolve.mutate(
      { id, approve },
      {
        onSuccess: () =>
          toast.success(
            t(
              approve ? "identityReviews.approved" : "identityReviews.rejected",
            ),
          ),
      },
    );
  };

  return (
    <>
      <TopBar
        title={t("identityReviews.title")}
        subtitle={t("identityReviews.subtitle")}
      />

      <div className="p-8">
        {isError ? (
          <Card className="p-6">
            <p className="text-error text-[14px]">
              {t("identityReviews.loadError", { message: error.message })}
            </p>
          </Card>
        ) : (
          <Card className="overflow-hidden">
            <Table>
              <Table.Head>
                <Table.HeaderCell>
                  {t("identityReviews.columns.person")}
                </Table.HeaderCell>
                <Table.HeaderCell>
                  {t("identityReviews.columns.organization")}
                </Table.HeaderCell>
                <Table.HeaderCell>
                  {t("identityReviews.columns.nameChange")}
                </Table.HeaderCell>
                <Table.HeaderCell>
                  {t("identityReviews.columns.requested")}
                </Table.HeaderCell>
                <Table.HeaderCell>
                  {t("identityReviews.columns.actions")}
                </Table.HeaderCell>
              </Table.Head>
              <Table.Body>
                {isPending ? (
                  <Table.State colSpan={5}>{t("common.loading")}</Table.State>
                ) : data.length === 0 ? (
                  <Table.State colSpan={5}>
                    {t("identityReviews.empty")}
                  </Table.State>
                ) : (
                  data.map((review) => {
                    const busy =
                      resolve.isPending && resolve.variables?.id === review.id;
                    return (
                      <Table.Row key={review.id}>
                        <Table.Cell className="text-ink">
                          {review.email}
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft">
                          {review.organizationName}
                        </Table.Cell>
                        <Table.Cell>
                          <div className="flex items-center gap-1.5">
                            <span className="text-ink-soft line-through">
                              {review.storedGivenNames} {review.storedLastName}
                            </span>
                            <Icon
                              name="arrow_front"
                              size={14}
                              className="text-muted"
                            />
                            <span className="text-ink font-semibold">
                              {review.disclosedGivenNames}{" "}
                              {review.disclosedLastName}
                            </span>
                          </div>
                        </Table.Cell>
                        <Table.Cell className="text-ink-soft text-[12.5px]">
                          {dateFormatter.format(new Date(review.createdAt))}
                        </Table.Cell>
                        <Table.Cell>
                          <div className="flex gap-2">
                            <Button
                              variant="secondary"
                              onClick={() => onResolve(review.id, false)}
                              loading={
                                busy && resolve.variables?.approve === false
                              }
                              disabled={resolve.isPending}
                            >
                              {t("identityReviews.reject")}
                            </Button>
                            <Button
                              variant="primary"
                              onClick={() => onResolve(review.id, true)}
                              loading={
                                busy && resolve.variables?.approve === true
                              }
                              disabled={resolve.isPending}
                            >
                              {t("identityReviews.approve")}
                            </Button>
                          </div>
                        </Table.Cell>
                      </Table.Row>
                    );
                  })
                )}
              </Table.Body>
            </Table>
          </Card>
        )}
      </div>
    </>
  );
}
