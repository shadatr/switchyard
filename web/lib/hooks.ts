"use client";

import { useMemo } from "react";
import useSWR, { type SWRConfiguration } from "swr";
import useSWRInfinite from "swr/infinite";
import type { ApiError, ApiKey, BreakdownDimension, RequestFilter, RequestsPage } from "./api";
import { usePrefs } from "./prefs";
import { useClient } from "./session";

// Keep showing the previous window's data while the next one loads, so
// periodic refreshes never flash skeletons.
const live: SWRConfiguration = { keepPreviousData: true, revalidateOnFocus: false };

export function useOverview() {
  const c = useClient();
  const { from, to, project } = usePrefs();
  return useSWR(["overview", from, to, project], () => c.overview({ from, to, project }), live);
}

export function useTimeseries() {
  const c = useClient();
  const { from, to, project } = usePrefs();
  return useSWR(["timeseries", from, to, project], () => c.timeseries({ from, to, project }), live);
}

export function useBreakdown(by: BreakdownDimension) {
  const c = useClient();
  const { from, to, project } = usePrefs();
  return useSWR(["breakdown", by, from, to, project], () => c.breakdown(by, { from, to, project }), live);
}

export function useKeys() {
  const c = useClient();
  return useSWR("keys", () => c.keys().then((r) => r.data), { revalidateOnFocus: true });
}

export function useTargets() {
  const c = useClient();
  return useSWR("targets", () => c.targets().then((r) => r.data), { refreshInterval: 5000 });
}

export function useRequestDetail(id: string | null) {
  const c = useClient();
  return useSWR(id ? ["request", id] : null, () => c.requestById(id as string));
}

/** Keyset-paginated request log. The window is frozen per filter set so
 * "Load more" keeps paging the same snapshot. */
export function useRequestLog(filter: Omit<RequestFilter, "cursor">) {
  const c = useClient();
  return useSWRInfinite<RequestsPage, ApiError>(
    (index, prev: RequestsPage | null) => {
      if (prev && !prev.next_cursor) return null;
      return ["requests", JSON.stringify(filter), index === 0 ? "" : (prev?.next_cursor ?? "")];
    },
    ([, , cursor]: [string, string, string]) => c.requests({ ...filter, cursor: cursor || undefined }),
    { revalidateFirstPage: false, revalidateOnFocus: false, keepPreviousData: true },
  );
}

/** id → key lookup for labelling key ids in logs. */
export function useKeyIndex() {
  const keys = useKeys();
  const data = keys.data;
  return useMemo(() => {
    const m = new Map<string, ApiKey>();
    for (const k of data ?? []) m.set(k.id, k);
    return (id: string) => m.get(id);
  }, [data]);
}
