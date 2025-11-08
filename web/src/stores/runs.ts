import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunDetail, RunSummary } from "@/types";
import {
  fetchRuns,
  fetchRunDetail,
  cancelRun as cancelRunRequest,
  cancelRunsBulk as cancelRunsBulkRequest
} from "@/api/runs";
import type { BulkRunCancelResponse, RunQueryParams } from "@/api/runs";

export interface RunFilters {
  status?: string;
  exploitId?: string;
  targetId?: string;
}

export const useRunStore = defineStore("runs", () => {
  const runs = ref<RunSummary[]>([]);
  const loading = ref(false);
  const selectedRun = ref<RunDetail | null>(null);
  const nextCursor = ref<string | null>(null);
  const hasMore = ref(true);
  const filters = ref<RunFilters>({});

  async function refresh() {
    runs.value = [];
    nextCursor.value = null;
    hasMore.value = true;
    await loadMore();
  }

  function buildRunQuery(cursor?: string | null): RunQueryParams {
    const params: RunQueryParams = {};
    if (cursor) {
      params.cursor = cursor;
    }
    if (filters.value.status) {
      params.status = filters.value.status;
    }
    if (filters.value.exploitId) {
      params.exploitId = filters.value.exploitId;
    }
    if (filters.value.targetId) {
      params.targetId = filters.value.targetId;
    }
    return params;
  }

  async function loadMore() {
    if (loading.value || !hasMore.value) {
      return;
    }
    loading.value = true;
    try {
      const params = buildRunQuery(nextCursor.value);
      const response = await fetchRuns(params);
      if (nextCursor.value === null) {
        runs.value = response.items;
      } else {
        runs.value = [...runs.value, ...response.items];
      }
      nextCursor.value = response.nextCursor;
      if (!nextCursor.value) {
        nextCursor.value = null;
        hasMore.value = false;
      } else {
        hasMore.value = true;
      }
    } catch (err) {
      console.error("Failed to load runs", err);
    } finally {
      loading.value = false;
    }
  }

  async function selectRun(id: string) {
    selectedRun.value = await fetchRunDetail(id);
  }

  function clearSelected() {
    selectedRun.value = null;
  }

  async function cancelRun(runId: string, reason?: string) {
    try {
      const updated = await cancelRunRequest(runId, reason ? { reason } : undefined);
      if (!updated) {
        return null;
      }
      applyRunUpdate(updated);
      return updated;
    } catch (err) {
      console.error("Failed to cancel run", err);
      return null;
    }
  }

  async function cancelRunsBulk(runIds: string[], reason?: string) {
    const uniqueIds = Array.from(new Set(runIds.filter((id) => id && id.length)));
    if (!uniqueIds.length) {
      return { errors: [] as string[] };
    }
    let response: BulkRunCancelResponse;
    try {
      response = await cancelRunsBulkRequest(uniqueIds, reason ? { reason } : undefined);
    } catch (err) {
      console.error("Failed to cancel runs", err);
      return { errors: ["request failed"] };
    }
    const errors: string[] = [];
    for (const result of response.results ?? []) {
      if (result.error) {
        const label = result.runId ?? "unknown";
        errors.push(`${label}: ${result.error}`);
        continue;
      }
      if (result.run) {
        applyRunUpdate(result.run);
      }
    }
    return { errors };
  }

  function applyRunUpdate(updated: RunSummary | RunDetail) {
    runs.value = runs.value.map((run) =>
      run.id === updated.id
        ? {
            ...run,
            status: updated.status,
            runnerId: updated.runnerId ?? run.runnerId,
            startedAt: updated.startedAt ?? run.startedAt,
            finishedAt: updated.finishedAt ?? run.finishedAt,
            queuedAt: updated.queuedAt ?? run.queuedAt,
            exploitId: updated.exploitId ?? run.exploitId,
            exploitName: updated.exploitName ?? run.exploitName,
            targetId: updated.targetId ?? run.targetId,
            targetLabel: updated.targetLabel ?? run.targetLabel,
            errorMessage: updated.errorMessage ?? run.errorMessage
          }
        : run
    );
    if (selectedRun.value?.id === updated.id) {
      selectedRun.value = { ...selectedRun.value, ...updated };
    }
  }

  function normalizeFilters(input: RunFilters): RunFilters {
    const result: RunFilters = {};
    if (input.status?.trim()) {
      result.status = input.status.trim();
    }
    if (input.exploitId?.trim()) {
      result.exploitId = input.exploitId.trim();
    }
    if (input.targetId?.trim()) {
      result.targetId = input.targetId.trim();
    }
    return result;
  }

  async function setFilters(next: RunFilters) {
    filters.value = normalizeFilters(next);
    await refresh();
  }

  return {
    runs,
    loading,
    selectedRun,
    hasMore,
    filters,
    refresh,
    loadMore,
    selectRun,
    clearSelected,
    cancelRun,
    cancelRunsBulk,
    setFilters
  };
});
