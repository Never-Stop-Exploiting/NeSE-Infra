import { defineStore } from "pinia";
import { ref } from "vue";
import type { LogEntry } from "@/types";
import { fetchLogs } from "@/api/logs";

export const useLogStore = defineStore("logs", () => {
  const logs = ref<LogEntry[]>([]);
  const loading = ref(false);
  const filters = ref<{ level?: string; source?: string; runId?: string; flagId?: string }>({});

  async function load() {
    loading.value = true;
    try {
      logs.value = await fetchLogs(filters.value);
    } catch (err) {
      console.error("Failed to load logs", err);
    } finally {
      loading.value = false;
    }
  }

  function setFilters(newFilters: typeof filters.value) {
    filters.value = { ...newFilters };
  }

  return { logs, loading, filters, setFilters, load };
});
