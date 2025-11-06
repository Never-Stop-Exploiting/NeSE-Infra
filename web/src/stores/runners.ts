import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunnerInfo } from "@/types";
import { fetchRunners, deleteRunner } from "@/api/runners";

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

  async function remove(id: string) {
    await deleteRunner(id);
    runners.value = runners.value.filter((runner) => runner.id !== id);
  }

  return { runners, loading, load, remove };
});
