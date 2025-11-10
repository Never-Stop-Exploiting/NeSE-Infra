import { defineStore } from "pinia";
import { ref } from "vue";
import type { RunnerInfo, SubmitterInfo } from "@/types";
import { fetchRunners, deleteRunner, fetchSubmitters, deleteSubmitter } from "@/api/runners";

export const useRunnerStore = defineStore("runners", () => {
  const runners = ref<RunnerInfo[]>([]);
  const submitters = ref<SubmitterInfo[]>([]);
  const loading = ref(false);

  async function load() {
    loading.value = true;
    try {
      const [runnerItems, submitterItems] = await Promise.all([fetchRunners(), fetchSubmitters()]);
      runners.value = runnerItems;
      submitters.value = submitterItems;
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

  return { runners, submitters, loading, load, remove, removeSubmitter };
});
