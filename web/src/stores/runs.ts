import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunDetail, RunSummary } from "@/types";
import { fetchRuns, fetchRunDetail, cancelRun as cancelRunRequest } from "@/api/runs";

export const useRunStore = defineStore("runs", () => {
  const runs = ref<RunSummary[]>([]);
  const loading = ref(false);
  const selectedRun = ref<RunDetail | null>(null);
  const nextCursor = ref<string | null>(null);
  const hasMore = ref(true);

  async function refresh() {
    runs.value = [];
    nextCursor.value = null;
    hasMore.value = true;
    await loadMore();
  }

  async function loadMore() {
    if (loading.value || !hasMore.value) {
      return;
    }
    loading.value = true;
    try {
      const response = await fetchRuns(
        nextCursor.value ? { cursor: nextCursor.value } : undefined
      );
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
      runs.value = runs.value.map((run) =>
        run.id === updated.id
          ? {
              ...run,
              status: updated.status,
              runnerId: updated.runnerId ?? run.runnerId,
              startedAt: updated.startedAt ?? run.startedAt,
              finishedAt: updated.finishedAt ?? run.finishedAt,
              queuedAt: updated.queuedAt ?? run.queuedAt
            }
          : run
      );
      if (selectedRun.value?.id === updated.id) {
        selectedRun.value = { ...selectedRun.value, ...updated };
      }
      return updated;
    } catch (err) {
      console.error("Failed to cancel run", err);
      return null;
    }
  }

  return {
    runs,
    loading,
    selectedRun,
    hasMore,
    refresh,
    loadMore,
    selectRun,
    clearSelected,
    cancelRun
  };
});
