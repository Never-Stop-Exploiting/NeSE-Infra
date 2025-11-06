import { api, fallback } from "./client";
import type { RunDetail, RunSummary } from "@/types";

const base = "/api/v1/runs";

export async function fetchRuns(params?: { cursor?: string; limit?: number }): Promise<{
  items: RunSummary[];
  nextCursor: string | null;
}> {
  const query = new URLSearchParams();
  if (params?.cursor) {
    query.set("cursor", params.cursor);
  }
  if (params?.limit) {
    query.set("limit", params.limit.toString());
  }
  const url = query.toString() ? `${base}?${query.toString()}` : base;
  return fallback(
    api.get<{ items: RunSummary[]; nextCursor?: string | null }>(url).then((res) => {
      const next = res.data.nextCursor ?? null;
      return {
        items: res.data.items ?? [],
        nextCursor: next && next.length > 0 ? next : null
      };
    }),
    { items: [], nextCursor: null }
  );
}

export async function fetchRunDetail(runId: string): Promise<RunDetail | null> {
  return fallback(api.get<{ run: RunDetail }>(`${base}/${runId}`).then((res) => res.data.run), null);
}
