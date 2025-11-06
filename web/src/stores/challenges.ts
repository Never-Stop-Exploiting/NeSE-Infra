import { defineStore } from "pinia";
import { ref } from "vue";
import type { Challenge } from "@/types";
import { fetchChallenges } from "@/api/challenges";

export const useChallengeStore = defineStore("challenges", () => {
  const challenges = ref<Challenge[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function load(): Promise<void> {
    if (loading.value) return;
    loading.value = true;
    error.value = null;
    try {
      challenges.value = await fetchChallenges();
    } catch (err) {
      console.error(err);
      error.value = "Failed to load challenges";
    } finally {
      loading.value = false;
    }
  }

  return {
    challenges,
    loading,
    error,
    load
  };
});
