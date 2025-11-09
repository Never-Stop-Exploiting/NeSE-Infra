import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type { SchedulerSettings, TargetSchedule } from "@/types";
import {
  fetchSchedulerSettings,
  fetchSchedulerTargets,
  updateSchedulerSettings,
  updateTargetExploits
} from "@/api/scheduler";

export const useSchedulerStore = defineStore("scheduler", () => {
  const settings = ref<SchedulerSettings | null>(null);
  const targets = ref<TargetSchedule[]>([]);
  const loading = ref(false);
  const savingSettings = ref(false);
  const updatingTargets = ref<Record<string, boolean>>({});
  const error = ref<string | null>(null);

  function setUpdating(targetId: string, value: boolean) {
    updatingTargets.value = { ...updatingTargets.value, [targetId]: value };
  }

  async function load() {
    loading.value = true;
    error.value = null;
    try {
      const [settingsData, targetsData] = await Promise.all([
        fetchSchedulerSettings(),
        fetchSchedulerTargets()
      ]);
      settings.value = settingsData;
      targets.value = targetsData;
    } catch (err) {
      console.error(err);
      error.value = "Failed to load scheduler data";
    } finally {
      loading.value = false;
    }
  }

  async function loadTargets() {
    try {
      targets.value = await fetchSchedulerTargets();
    } catch (err) {
      console.error(err);
      error.value = "Failed to load scheduler targets";
    }
  }

  async function saveSettings(payload: {
    roundIntervalMinutes?: number;
    roundIntervalSeconds?: number;
    maxDispatchBatch?: number;
    enabled?: boolean;
    strategy?: SchedulerSettings["strategy"];
    continuousDelaySeconds?: number;
  }) {
    if (savingSettings.value) return;
    savingSettings.value = true;
    error.value = null;
    try {
      const updated = await updateSchedulerSettings(payload);
      settings.value = updated;
    } catch (err) {
      console.error(err);
      error.value = "Failed to update scheduler settings";
      throw err;
    } finally {
      savingSettings.value = false;
    }
  }

  async function setEnabled(enabled: boolean) {
    await saveSettings({ enabled });
  }

  async function setTargetStatuses(
    targetId: string,
    statuses: { exploitId: string; enabled: boolean }[]
  ) {
    if (!statuses.length) return;
    setUpdating(targetId, true);
    error.value = null;
    try {
      const updated = await updateTargetExploits(targetId, statuses);
      targets.value = targets.value.map((item) =>
        item.target.id === updated.target.id ? updated : item
      );
    } catch (err) {
      console.error(err);
      error.value = "Failed to update target overrides";
      throw err;
    } finally {
      setUpdating(targetId, false);
    }
  }

  const groupedTargets = computed(() => {
    const groups = new Map<
      string,
      { challengeId: string; challengeName: string; targets: TargetSchedule[] }
    >();
    for (const item of targets.value) {
      const challengeId = item.challenge?.id ?? "unassigned";
      const challengeName = item.challenge?.name ?? "Unassigned Challenge";
      if (!groups.has(challengeId)) {
        groups.set(challengeId, { challengeId, challengeName, targets: [] });
      }
      groups.get(challengeId)!.targets.push(item);
    }
    return Array.from(groups.values()).sort((a, b) =>
      a.challengeName.localeCompare(b.challengeName)
    );
  });

  return {
    settings,
    targets,
    groupedTargets,
    loading,
    savingSettings,
    updatingTargets,
    error,
    load,
    loadTargets,
    saveSettings,
    setEnabled,
    setTargetStatuses
  };
});
