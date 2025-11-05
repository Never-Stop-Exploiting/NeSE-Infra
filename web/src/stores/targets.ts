import { defineStore } from "pinia";
import { ref } from "vue";
import type { Target } from "@/types";
import { fetchTargets, createTarget, importTargets } from "@/api/targets";

export const useTargetStore = defineStore("targets", () => {
  const targets = ref<Target[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function load() {
    loading.value = true;
    error.value = null;
    try {
      targets.value = await fetchTargets();
    } catch (err) {
      error.value = "Failed to load targets";
      console.error(err);
    } finally {
      loading.value = false;
    }
  }

  async function addTarget(target: Omit<Target, "id">) {
    const created = await createTarget(target);
    targets.value.push(created);
  }

  async function bulkImport(payload: {
    challengeId: string;
    baseIp: string;
    port: number;
    teamNames?: string[];
    count: number;
  }) {
    await importTargets(payload);
  }

  return { targets, loading, error, load, addTarget, bulkImport };
});
