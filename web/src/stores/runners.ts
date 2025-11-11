import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunnerInfo, SubmitterInfo } from "@/types";
import { fetchRunners, deleteRunner, fetchSubmitters, deleteSubmitter } from "@/api/runners";

export const useRunnerStore = defineStore("runners", () => {
  const runners = ref<RunnerInfo[]>([]);
  const submitters = ref<SubmitterInfo[]>([]);
  const submitterSummary = ref<{ pressure?: number } | null>(null);
  const loading = ref(false);

  async function load() {
    loading.value = true;
    try {
      const [runnerItems, submitterPayload] = await Promise.all([fetchRunners(), fetchSubmitters()]);
      runners.value = runnerItems;
      submitters.value = submitterPayload.items;
      submitterSummary.value = submitterPayload.summary ?? null;
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

  async function removeSubmitter(id: string) {
    await deleteSubmitter(id);
    submitters.value = submitters.value.filter((submitter) => submitter.id !== id);
  }

  return { runners, submitters, submitterSummary, loading, load, remove, removeSubmitter };
});
