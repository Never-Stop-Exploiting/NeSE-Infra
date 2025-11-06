import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunDetail, RunSummary } from "@/types";
import { fetchRuns, fetchRunDetail } from "@/api/runs";

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

  return { runs, loading, selectedRun, hasMore, refresh, loadMore, selectRun, clearSelected };
});
