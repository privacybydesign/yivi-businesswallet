import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { getHealth } from "./health";
import type { HealthResponse } from "./health";

export const healthQueryKey = ["health"] as const;

export function useHealthQuery(): UseQueryResult<HealthResponse, Error> {
  return useQuery({
    queryKey: healthQueryKey,
    queryFn: ({ signal }) => getHealth(signal),
  });
}
