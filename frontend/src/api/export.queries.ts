import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { createExportJob, listExportJobs, setDataInstruction } from "./export";
import type { DataInstruction, ExportJob, ExportSection } from "./export";
import type { Organization } from "./organization";
import {
  organizationAuditEventsQueryKey,
  organizationQueryKey,
} from "./organization.queries";
import { toast } from "../lib/toast";

// How often the list is refetched while a bundle is being assembled. An export
// is minutes of work at worst, so the screen polls rather than holding a request
// open.
const RUNNING_POLL_MS = 3000;

export function exportJobsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "export", "jobs"];
}

export function useExportJobsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<ExportJob[], Error> {
  return useQuery({
    queryKey: exportJobsQueryKey(slug),
    queryFn: ({ signal }) => listExportJobs(slug, signal),
    enabled: enabled && slug !== "",
    // Polling stops as soon as nothing is in flight, so an idle screen is quiet.
    refetchInterval: (query) =>
      (query.state.data ?? []).some(
        (job) => job.status === "queued" || job.status === "running",
      )
        ? RUNNING_POLL_MS
        : false,
  });
}

export function useCreateExportJobMutation(
  slug: string,
): UseMutationResult<ExportJob, Error, ExportSection[]> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (sections) => createExportJob(slug, sections),
    onSuccess: () => {
      toast.success(t("toasts.exportQueued"));
      void queryClient.invalidateQueries({
        queryKey: exportJobsQueryKey(slug),
      });
      // Queueing is the audited act, so the trail has a new entry already.
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useSetDataInstructionMutation(
  slug: string,
): UseMutationResult<Organization, Error, DataInstruction> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (instruction) => setDataInstruction(slug, instruction),
    onSuccess: () => {
      toast.success(t("toasts.dataInstructionSaved"));
      void queryClient.invalidateQueries({
        queryKey: organizationQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}
