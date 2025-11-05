import { defineStore } from "pinia";
import { ref } from "vue";
import type { FlagRecord } from "@/types";
import { fetchFlags, retryFlag, fetchFlagLogs } from "@/api/flags";

export const useFlagStore = defineStore("flags", () => {
  const flags = ref<FlagRecord[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const logCache = ref<Record<string, string[]>>({});

  async function load() {
    loading.value = true;
    error.value = null;
    try {
      flags.value = await fetchFlags();
    } catch (err) {
      error.value = "Failed to load flags";
      console.error(err);
    } finally {
      loading.value = false;
    }
  }

  async function retry(id: string) {
    await retryFlag(id);
  }

  async function loadLogs(id: string) {
    if (logCache.value[id]) {
      return logCache.value[id];
    }
    const { logs } = await fetchFlagLogs(id);
    logCache.value[id] = logs;
    return logs;
  }

  return { flags, loading, error, load, retry, loadLogs };
});
