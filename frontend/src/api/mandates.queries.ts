import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type {
  GrantMandateInput,
  Mandate,
  MandateAuthority,
  RevokeMandateInput,
} from "./mandates";
import {
  getMandateAuthority,
  getMandates,
  grantMandate,
  revokeMandate,
} from "./mandates";
import { organizationAuditEventsQueryKey } from "./organization.queries";
import { toast } from "../lib/toast";

export function mandatesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "mandates"];
}

export function mandateAuthorityQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "mandates", "authority"];
}

export function useMandatesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<Mandate[], Error> {
  return useQuery({
    queryKey: mandatesQueryKey(slug),
    queryFn: ({ signal }) => getMandates(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useMandateAuthorityQuery(
  slug: string,
  enabled = true,
): UseQueryResult<MandateAuthority, Error> {
  return useQuery({
    queryKey: mandateAuthorityQueryKey(slug),
    queryFn: ({ signal }) => getMandateAuthority(slug, signal),
    enabled: enabled && slug !== "",
  });
}

// A grant or a revocation can change the caller's own basis of authority — a
// full mandate granted onward, or their own revoked — so the authority behind the
// action buttons is refetched alongside the register.
function useMandateWriteSettled(slug: string): () => void {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: mandatesQueryKey(slug) });
    void queryClient.invalidateQueries({
      queryKey: mandateAuthorityQueryKey(slug),
    });
    void queryClient.invalidateQueries({
      queryKey: organizationAuditEventsQueryKey(slug),
    });
  };
}

export function useGrantMandateMutation(
  slug: string,
): UseMutationResult<Mandate, Error, GrantMandateInput> {
  const { t } = useTranslation();
  const settled = useMandateWriteSettled(slug);
  return useMutation({
    mutationFn: (input) => grantMandate(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.mandateGranted"));
      settled();
    },
  });
}

export function useRevokeMandateMutation(
  slug: string,
): UseMutationResult<
  Mandate[],
  Error,
  { mandateId: string } & RevokeMandateInput
> {
  const { t } = useTranslation();
  const settled = useMandateWriteSettled(slug);
  return useMutation({
    mutationFn: ({ mandateId, ...input }) =>
      revokeMandate(slug, mandateId, input),
    meta: { suppressErrorToast: true },
    onSuccess: (reached, variables) => {
      // An effective-dated revocation has not ended anything yet: the mandates
      // stay active until the date and expire on their own.
      toast.success(
        variables.effectiveAt === undefined
          ? t("toasts.mandateRevoked", { count: reached.length })
          : t("toasts.mandateRevocationScheduled", { count: reached.length }),
      );
      settled();
    },
  });
}
