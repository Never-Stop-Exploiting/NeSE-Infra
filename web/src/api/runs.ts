import { api, fallback } from "./client";
import type { RunDetail, RunSummary } from "@/types";

const base = "/api/v1/runs";

export interface RunQueryParams {
  cursor?: string;
  limit?: number;
  status?: string;
  exploitName?: string;
  targetName?: string;
  challengeId?: string;
}

export interface BulkRunCancelResponse {
  results: Array<{
    run?: RunSummary;
    runId?: string;
    error?: string;
  }>;
}

export async function fetchRuns(params?: RunQueryParams): Promise<{
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
  if (params?.status) {
    query.set("status", params.status);
  }
  if (params?.exploitName) {
    query.set("exploitName", params.exploitName);
  }
  if (params?.targetName) {
    query.set("targetName", params.targetName);
  }
  if (params?.challengeId) {
    query.set("challengeId", params.challengeId);
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

export async function cancelRun(
  runId: string,
  payload?: { reason?: string }
): Promise<RunDetail | null> {
  const body = payload?.reason ? payload : {};
  return fallback(
    api.post<{ run: RunDetail }>(`${base}/${runId}/cancel`, body).then((res) => res.data.run),
    null
  );
}

export async function cancelRunsBulk(
  runIds: string[],
  payload?: { reason?: string }
): Promise<BulkRunCancelResponse> {
  return fallback(
    api
      .post<BulkRunCancelResponse>(`${base}/cancel`, {
        runIds,
        ...(payload?.reason ? { reason: payload.reason } : {})
      })
      .then((res) => ({
        results: res.data.results ?? []
      })),
    { results: [] }
  );
}
