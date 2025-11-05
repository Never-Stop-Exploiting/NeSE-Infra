import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunnerInfo } from "@/types";
import { fetchRunners } from "@/api/runners";

export const useRunnerStore = defineStore("runners", () => {
  const runners = ref<RunnerInfo[]>([]);
  const loading = ref(false);

  async function load() {
    loading.value = true;
    try {
      runners.value = await fetchRunners();
    } catch (err) {
      console.error("Failed to load runners", err);
    } finally {
      loading.value = false;
    }
  }

  return { runners, loading, load };
});
