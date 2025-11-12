import { defineStore } from "pinia";
import { ref } from "vue";
import type { FlagRecord } from "@/types";
import {
  fetchFlags,
  retryFlag,
  fetchFlagLogs,
  cancelFlag as apiCancelFlag,
  cancelFlags,
  retryFlags,
  type FlagQueryParams
} from "@/api/flags";

export interface FlagFilters {
  status?: string;
  challengeName?: string;
  targetName?: string;
}

export const useFlagStore = defineStore("flags", () => {
  const flags = ref<FlagRecord[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const logCache = ref<Record<string, string[]>>({});
  const nextCursor = ref<string | null>(null);
  const hasMore = ref(true);
  const filters = ref<FlagFilters>({});

  async function refresh() {
    flags.value = [];
    nextCursor.value = null;
    hasMore.value = true;
    await loadMore();
  }

  async function load() {
    await refresh();
  }

  function buildQuery(cursor?: string | null): FlagQueryParams {
    const params: FlagQueryParams = {};
    if (cursor) {
      params.cursor = cursor;
    }
    if (filters.value.status) {
      params.status = filters.value.status;
    }
    if (filters.value.challengeName) {
      params.challengeName = filters.value.challengeName;
    }
    if (filters.value.targetName) {
      params.targetName = filters.value.targetName;
    }
    return params;
  }

  async function loadMore() {
    if (loading.value || !hasMore.value) {
      return;
    }
    loading.value = true;
    error.value = null;
    try {
      const response = await fetchFlags(buildQuery(nextCursor.value));
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

  function normalizeFilters(input: FlagFilters): FlagFilters {
    const normalized: FlagFilters = {};
    if (input.status?.trim()) {
      normalized.status = input.status.trim();
    }
    if (input.challengeName?.trim()) {
      normalized.challengeName = input.challengeName.trim();
    }
    if (input.targetName?.trim()) {
      normalized.targetName = input.targetName.trim();
    }
    return normalized;
  }

  async function setFilters(next: FlagFilters) {
    filters.value = normalizeFilters(next);
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

  async function retrySelected(ids: string[]) {
    if (!ids.length) {
      return { results: [] };
    }
    const response = await retryFlags(ids);
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
    filters,
    load,
    refresh,
    loadMore,
    setFilters,
    retry,
    loadLogs,
    cancel,
    cancelSelected,
    retrySelected
  };
});
