import { defineStore } from "pinia";
import { ref } from "vue";
import type { Challenge } from "@/types";
import {
  fetchChallenges,
  createChallenge as apiCreateChallenge,
  updateChallenge as apiUpdateChallenge,
  deleteChallenge as apiDeleteChallenge
} from "@/api/challenges";

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

  async function create(payload: {
    name: string;
    category?: string;
    serviceType?: string;
    description?: string;
    flagRegex?: string;
    metadata?: Record<string, unknown>;
  }): Promise<Challenge> {
    const challenge = await apiCreateChallenge(payload);
    challenges.value.push(challenge);
    return challenge;
  }

  async function update(
    id: string,
    payload: {
      name?: string;
      category?: string;
      serviceType?: string;
      description?: string;
      flagRegex?: string;
      metadata?: Record<string, unknown>;
    }
  ): Promise<Challenge> {
    const updated = await apiUpdateChallenge(id, payload);
    challenges.value = challenges.value.map((challenge) =>
      challenge.id === id ? updated : challenge
    );
    return updated;
  }

  async function remove(id: string): Promise<void> {
    await apiDeleteChallenge(id);
    challenges.value = challenges.value.filter((challenge) => challenge.id !== id);
  }

  return {
    challenges,
    loading,
    error,
    load,
    create,
    update,
    remove
  };
});
