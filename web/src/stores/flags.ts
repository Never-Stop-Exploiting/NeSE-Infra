import { defineStore } from "pinia";
import { ref } from "vue";
import type { FlagRecord } from "@/types";
import { fetchFlags, retryFlag, fetchFlagLogs, cancelFlag as apiCancelFlag, cancelFlags } from "@/api/flags";

export const useFlagStore = defineStore("flags", () => {
  const flags = ref<FlagRecord[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const logCache = ref<Record<string, string[]>>({});
  const nextCursor = ref<string | null>(null);
  const hasMore = ref(true);
  const statusFilter = ref<string>("");

  async function refresh() {
    flags.value = [];
    nextCursor.value = null;
    hasMore.value = true;
    await loadMore();
  }

  async function load() {
    await refresh();
  }

  async function loadMore() {
    if (loading.value || !hasMore.value) {
      return;
    }
    loading.value = true;
    error.value = null;
    try {
      const response = await fetchFlags({
        status: statusFilter.value || undefined,
        cursor: nextCursor.value || undefined
      });
      if (!nextCursor.value) {
        flags.value = response.items;
      } else {
        flags.value = [...flags.value, ...response.items];
      }
      nextCursor.value = response.nextCursor ?? null;
      hasMore.value = !!nextCursor.value;
    } catch (err) {
      error.value = "Failed to load flags";
      console.error(err);
    } finally {
      loading.value = false;
    }
  }

  async function setStatusFilter(status: string) {
    if (statusFilter.value === status) {
      return;
    }
    statusFilter.value = status;
    await refresh();
  }

  async function retry(id: string) {
    await retryFlag(id);
    delete logCache.value[id];
    await refresh();
  }

  async function cancel(id: string, reason?: string) {
    await apiCancelFlag(id, reason);
    delete logCache.value[id];
    await refresh();
  }

  async function cancelSelected(ids: string[], reason?: string) {
    if (!ids.length) {
      return { results: [] };
    }
    const response = await cancelFlags(ids, reason);
    for (const result of response.results ?? []) {
      if (result.flagId) {
        delete logCache.value[result.flagId];
      }
    }
    await refresh();
    return response;
  }

  async function loadLogs(id: string) {
    if (logCache.value[id]) {
      return logCache.value[id];
    }
    const { logs } = await fetchFlagLogs(id);
    logCache.value[id] = logs;
    return logs;
  }

  return {
    flags,
    loading,
    error,
    hasMore,
    statusFilter,
    load,
    refresh,
    loadMore,
    setStatusFilter,
    retry,
    loadLogs,
    cancel,
    cancelSelected
  };
});
