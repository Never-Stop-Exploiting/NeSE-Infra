import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunDetail, RunSummary } from "@/types";
import { fetchRuns, fetchRunDetail } from "@/api/runs";

export const useRunStore = defineStore("runs", () => {
  const runs = ref<RunSummary[]>([]);
  const loading = ref(false);
  const selectedRun = ref<RunDetail | null>(null);

  async function load() {
    loading.value = true;
    try {
      runs.value = await fetchRuns();
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

  return { runs, loading, selectedRun, load, selectRun, clearSelected };
});
