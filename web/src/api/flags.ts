import { api, fallback } from "./client";
import type { FlagRecord } from "@/types";

const base = "/api/v1/flags";

export interface BulkFlagActionResponse {
  results: Array<{
    flagId?: string;
    status?: string;
    error?: string;
  }>;
}

export interface FlagQueryParams {
  status?: string;
  cursor?: string;
  limit?: number;
  challengeName?: string;
  targetName?: string;
}

export async function fetchFlags(params?: FlagQueryParams): Promise<{ items: FlagRecord[]; nextCursor: string | null }> {
  const query = new URLSearchParams();
  if (params?.status) {
    query.set("status", params.status);
  }
  if (params?.cursor) {
    query.set("cursor", params.cursor);
  }
  if (params?.limit) {
    query.set("limit", params.limit.toString());
  }
  if (params?.challengeName) {
    query.set("challengeName", params.challengeName);
  }
  if (params?.targetName) {
    query.set("targetName", params.targetName);
  }
  const url = query.toString() ? `${base}?${query.toString()}` : base;
  return fallback(
    api.get<{ items: FlagRecord[]; nextCursor?: string | null }>(url).then((res) => ({
      items: res.data.items ?? [],
      nextCursor: res.data.nextCursor ?? null
    })),
    { items: [], nextCursor: null }
  );
}

export async function fetchFlagDetail(flagId: string): Promise<FlagRecord | null> {
  return fallback(api.get<{ flag: FlagRecord }>(`${base}/${flagId}`).then((res) => res.data.flag), null);
}

export async function retryFlag(flagId: string): Promise<void> {
  await api.post(`${base}/${flagId}/requeue`);
}

export async function cancelFlag(flagId: string, reason?: string): Promise<void> {
  const payload = reason ? { reason } : {};
  await api.post(`${base}/${flagId}/cancel`, payload);
}

export async function cancelFlags(flagIds: string[], reason?: string): Promise<BulkFlagActionResponse> {
  const payload: { flagIds: string[]; reason?: string } = { flagIds };
  if (reason) {
    payload.reason = reason;
  }
  return fallback(
    api.post<BulkFlagActionResponse>(`${base}/cancel`, payload).then((res) => ({
      results: res.data.results ?? []
    })),
    { results: [] }
  );
}

export async function retryFlags(flagIds: string[]): Promise<BulkFlagActionResponse> {
  const payload = { flagIds };
  return fallback(
    api.post<BulkFlagActionResponse>(`${base}/retry`, payload).then((res) => ({
      results: res.data.results ?? []
    })),
    { results: [] }
  );
}

export async function fetchFlagLogs(flagId: string): Promise<{ logs: string[] }> {
  return fallback(api.get<{ logs: string[] }>(`${base}/${flagId}/logs`).then((res) => res.data), {
    logs: []
  });
}
