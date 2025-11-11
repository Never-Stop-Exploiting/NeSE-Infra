<template>
  <section class="grid">
    <article class="panel settings">
      <header class="settings-header">
        <div class="heading">
          <h2>Scheduler Settings</h2>
          <p>Configure dispatch interval and batch size for exploit runs.</p>
        </div>
        <button
          type="button"
          class="toggle"
          :class="{ active: isEnabled }"
          @click="handleToggleScheduler"
          :disabled="savingSettings"
        >
          {{ isEnabled ? "Pause Scheduler" : "Start Scheduler" }}
        </button>
      </header>
      <form class="form" @submit.prevent="handleSaveSettings">
        <p class="scheduler-status" :class="{ active: isEnabled }">
          Scheduler {{ isEnabled ? "Active" : "Paused" }}
        </p>
        <label>
          Strategy
          <select v-model="settingsForm.strategy" :disabled="savingSettings">
            <option value="interval">Round interval</option>
            <option value="continuous">Continuous per exploit</option>
          </select>
          <small class="hint">
            Interval batches all targets; continuous reruns each exploit for every target as soon as it finishes.
          </small>
        </label>
        <label v-if="isIntervalStrategy">
          Round Interval (seconds)
          <input
            v-model.number="settingsForm.roundIntervalSeconds"
            type="number"
            min="1"
            required
            :disabled="savingSettings"
          />
          <small class="hint">≈ {{ roundIntervalPreview }}</small>
        </label>
        <label v-else>
          Continuous Delay (seconds)
          <input
            v-model.number="settingsForm.continuousDelaySeconds"
            type="number"
            min="0"
            required
            :disabled="savingSettings"
          />
          <small class="hint">{{ continuousDelayPreview }}</small>
        </label>
        <label>
          Max Dispatch Batch
          <input
            v-model.number="settingsForm.maxDispatchBatch"
            type="number"
            min="1"
            required
            :disabled="savingSettings"
          />
        </label>
        <button type="submit" :disabled="savingSettings">
          {{ savingSettings ? "Saving..." : "Save Settings" }}
        </button>
        <p v-if="settings?.updatedAt" class="meta">
          Last updated: {{ new Date(settings.updatedAt).toLocaleString() }}
        </p>
      </form>
    </article>

    <article v-if="error" class="panel warning">
      <header>
        <h2>Warning</h2>
      </header>
      <p>{{ error }}</p>
    </article>

    <article v-if="loading" class="panel placeholder">
      <p>Loading scheduler data…</p>
    </article>

    <article
      v-for="group in groupedTargets"
      v-else
      :key="group.challengeId"
      class="panel group"
      :class="{ collapsed: !isGroupExpanded(group.challengeId) }"
    >
      <header class="group-header">
        <div>
          <h2>{{ group.challengeName }}</h2>
          <p>{{ group.targets.length }} targets</p>
        </div>
        <button class="ghost" type="button" @click="toggleGroup(group.challengeId)">
          {{ isGroupExpanded(group.challengeId) ? "Collapse" : "Expand" }}
        </button>
      </header>
      <div class="target-list" v-if="group.targets.length && isGroupExpanded(group.challengeId)">
        <div
          v-for="item in group.targets"
          :key="item.target.id"
          class="target-card"
        >
          <button
            type="button"
            class="target-summary"
            @click="toggleExpanded(item.target.id)"
          >
            <div class="info">
              <strong>{{ item.target.teamName }}</strong>
              <span>{{ item.target.ip }}:{{ item.target.port }}</span>
            </div>
            <div class="status">
              <span class="dots">{{ statusPreview(item.summary.indicators) }}</span>
              <span class="counts">{{ item.summary.enabled }}/{{ item.summary.total }}</span>
            </div>
            <span class="chevron" :class="{ open: isExpanded(item.target.id) }">⌄</span>
          </button>
          <div v-if="isExpanded(item.target.id)" class="target-details">
            <div class="details-header">
              <div v-if="thumbnail(item)" class="thumb">
                <img :src="thumbnail(item)" alt="Target thumbnail" />
              </div>
              <div class="actions">
                <button
                  type="button"
                  @click.stop="handleBulk(item, true)"
                  :disabled="isTargetUpdating(item.target.id)"
                >
                  Enable All
                </button>
                <button
                  type="button"
                  @click.stop="handleBulk(item, false)"
                  :disabled="isTargetUpdating(item.target.id)"
                  class="secondary"
                >
                  Disable All
                </button>
              </div>
            </div>
            <ul class="exploit-grid">
              <li v-for="status in item.exploits" :key="status.exploitId" class="exploit-card">
                <input
                  type="checkbox"
                  :checked="status.enabled"
                  :disabled="isTargetUpdating(item.target.id)"
                  @change="handleToggle(item, status, $event)"
                />
                <span class="name">{{ status.exploitName }}</span>
                <span v-if="status.enabled !== status.defaultEnabled" class="badge">Override</span>
              </li>
            </ul>
          </div>
        </div>
      </div>
      <p v-else class="empty">No exploits configured for this challenge.</p>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { storeToRefs } from "pinia";
import type { SchedulerSettings, TargetExploitStatus, TargetSchedule } from "@/types";
import { useSchedulerStore } from "@/stores/scheduler";

const schedulerStore = useSchedulerStore();
const { settings, groupedTargets, loading, savingSettings, updatingTargets, error } =
  storeToRefs(schedulerStore);

type SchedulerForm = {
  strategy: SchedulerSettings["strategy"];
  roundIntervalSeconds: number;
  continuousDelaySeconds: number;
  maxDispatchBatch: number;
};

const settingsForm = reactive<SchedulerForm>({
  strategy: "interval",
  roundIntervalSeconds: 300,
  continuousDelaySeconds: 0,
  maxDispatchBatch: 50
});

const isEnabled = computed(() => settings.value?.enabled ?? false);
const isIntervalStrategy = computed(() => settingsForm.strategy === "interval");
const isContinuousStrategy = computed(() => settingsForm.strategy === "continuous");

watch(
  settings,
  (value) => {
    if (value) {
      settingsForm.strategy = (value.strategy as SchedulerSettings["strategy"]) || "interval";
      if (value.roundIntervalSeconds && value.roundIntervalSeconds > 0) {
        settingsForm.roundIntervalSeconds = value.roundIntervalSeconds;
      } else {
        settingsForm.roundIntervalSeconds = Math.max(value.roundIntervalMinutes || 0, 1) * 60;
      }
      settingsForm.continuousDelaySeconds = Math.max(value.continuousDelaySeconds || 0, 0);
      settingsForm.maxDispatchBatch = value.maxDispatchBatch;
    }
  },
  { immediate: true }
);

const expanded = ref<Record<string, boolean>>({});
const expandedGroups = ref<Record<string, boolean>>({});

function isExpanded(id: string) {
  return !!expanded.value[id];
}

function toggleExpanded(id: string) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
}

watch(
  groupedTargets,
  (groups) => {
    const next: Record<string, boolean> = { ...expandedGroups.value };
    groups.forEach((group) => {
      if (next[group.challengeId] === undefined) {
        next[group.challengeId] = true;
      }
    });
    expandedGroups.value = next;
  },
  { immediate: true }
);

function isGroupExpanded(id: string) {
  return expandedGroups.value[id] ?? true;
}

function toggleGroup(id: string) {
  expandedGroups.value = { ...expandedGroups.value, [id]: !isGroupExpanded(id) };
}

function statusPreview(indicators: string) {
  if (!indicators) return "";
  const chars = Array.from(indicators);
  const preview = chars.slice(0, 20).join("");
  return chars.length > 20 ? `${preview}…` : preview;
}

function thumbnail(item: TargetSchedule) {
  const labels = item.target.labels ?? {};
  return labels.thumbnail || labels.thumb || labels.image || labels.screenshot || "";
}

function isTargetUpdating(id: string) {
  return !!updatingTargets.value[id];
}

const roundIntervalPreview = computed(() => {
  const seconds = Math.max(0, Number(settingsForm.roundIntervalSeconds) || 0);
  if (seconds === 0) {
    return "0 seconds";
  }
  if (seconds < 60) {
    return `${seconds} second${seconds === 1 ? "" : "s"}`;
  }
  const minutes = seconds / 60;
  const formatted = minutes >= 10 ? minutes.toFixed(1) : minutes.toFixed(2);
  return `${formatted} minute${minutes === 1 ? "" : "s"}`;
});

const continuousDelayPreview = computed(() => {
  const seconds = Math.max(0, Number(settingsForm.continuousDelaySeconds) || 0);
  if (seconds === 0) {
    return "back-to-back runs";
  }
  if (seconds < 60) {
    return `${seconds} second${seconds === 1 ? "" : "s"}`;
  }
  const minutes = seconds / 60;
  const formatted = minutes >= 10 ? minutes.toFixed(1) : minutes.toFixed(2);
  return `${formatted} minute${minutes === 1 ? "" : "s"}`;
});

async function handleSaveSettings() {
  try {
    const payload: Parameters<typeof schedulerStore.saveSettings>[0] = {
      maxDispatchBatch: settingsForm.maxDispatchBatch,
      strategy: settingsForm.strategy
    };
    if (isIntervalStrategy.value) {
      const intervalSeconds = Math.max(
        1,
        Math.round(Number(settingsForm.roundIntervalSeconds) || 0)
      );
      settingsForm.roundIntervalSeconds = intervalSeconds;
      payload.roundIntervalSeconds = intervalSeconds;
    } else {
      const delaySeconds = Math.max(
        0,
        Math.round(Number(settingsForm.continuousDelaySeconds) || 0)
      );
      settingsForm.continuousDelaySeconds = delaySeconds;
      payload.continuousDelaySeconds = delaySeconds;
    }
    await schedulerStore.saveSettings(payload);
  } catch (err) {
    // errors are captured and exposed via store
    console.error(err);
  }
}

async function handleToggleScheduler() {
  try {
    await schedulerStore.setEnabled(!isEnabled.value);
  } catch (err) {
    console.error(err);
  }
}

async function handleToggle(
  item: TargetSchedule,
  status: TargetExploitStatus,
  event: Event
) {
  const checkbox = event.target as HTMLInputElement;
  try {
    await schedulerStore.setTargetStatuses(item.target.id, [
      { exploitId: status.exploitId, enabled: checkbox.checked }
    ]);
  } catch (err) {
    console.error(err);
    checkbox.checked = status.enabled;
  }
}

async function handleBulk(item: TargetSchedule, enabled: boolean) {
  if (!item.exploits.length) return;
  try {
    await schedulerStore.setTargetStatuses(
      item.target.id,
      item.exploits.map((status) => ({
        exploitId: status.exploitId,
        enabled
      }))
    );
  } catch (err) {
    console.error(err);
  }
}

onMounted(() => {
  schedulerStore.load();
});
</script>

<style scoped>
.grid {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.panel {
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.25);
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.settings .form {
  display: grid;
  gap: 1rem;
  max-width: 320px;
}

.settings-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.settings .heading {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.settings .heading h2 {
  margin: 0;
}

.settings .heading p {
  margin: 0;
  color: rgba(226, 232, 240, 0.75);
}

.settings .toggle {
  background: rgba(74, 222, 128, 0.18);
  color: #4ade80;
  border: 1px solid rgba(74, 222, 128, 0.35);
  padding: 0.55rem 1.2rem;
}

.settings .toggle.active {
  background: rgba(248, 113, 113, 0.18);
  color: #fca5a5;
  border-color: rgba(248, 113, 113, 0.35);
}

.scheduler-status {
  margin: 0;
  font-size: 0.85rem;
  color: rgba(148, 163, 184, 0.8);
}

.scheduler-status.active {
  color: #4ade80;
}

label {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  font-size: 0.9rem;
}

.hint {
  color: rgba(148, 163, 184, 0.8);
  font-size: 0.8rem;
}

input[type="number"],
select {
  padding: 0.6rem 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  background: rgba(15, 23, 42, 0.85);
  color: #f8fafc;
}

select {
  appearance: none;
  background-image: linear-gradient(45deg, transparent 50%, rgba(148, 163, 184, 0.5) 50%),
    linear-gradient(135deg, rgba(148, 163, 184, 0.5) 50%, transparent 50%);
  background-position: calc(100% - 18px) calc(50% - 3px), calc(100% - 12px) calc(50% - 3px);
  background-size: 6px 6px, 6px 6px;
  background-repeat: no-repeat;
  padding-right: 2rem;
}

button {
  background: #38bdf8;
  color: #0f172a;
  border: none;
  padding: 0.6rem 1.4rem;
  border-radius: 999px;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.15s ease;
}

button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

button.secondary {
  background: rgba(148, 163, 184, 0.2);
  color: #e2e8f0;
}

button.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.4);
  color: #94a3b8;
  border-radius: 8px;
  padding: 0.4rem 0.9rem;
}

button.ghost:hover:not(:disabled) {
  color: #e2e8f0;
  border-color: rgba(148, 163, 184, 0.7);
}

.meta {
  font-size: 0.8rem;
  color: rgba(226, 232, 240, 0.75);
}

.warning {
  border-color: rgba(248, 113, 113, 0.35);
  background: rgba(248, 113, 113, 0.08);
}

.placeholder {
  align-items: center;
  justify-content: center;
}

.target-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.group-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
}

.group-header h2 {
  margin: 0;
}

.target-card {
  background: rgba(15, 23, 42, 0.5);
  border-radius: 12px;
  border: 1px solid rgba(148, 163, 184, 0.15);
  overflow: hidden;
}

.target-summary {
  width: 100%;
  background: none;
  border: none;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 0.75rem;
  padding: 1rem 1.2rem;
  text-align: left;
  color: inherit;
}

.target-summary .info {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.target-summary .status {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.9rem;
}

.target-summary .dots {
  letter-spacing: 0.05rem;
}

.target-summary .counts {
  font-size: 0.85rem;
  color: rgba(148, 163, 184, 0.8);
}

.chevron {
  font-size: 1.1rem;
  transform: rotate(-90deg);
  transition: transform 0.2s ease;
}

.chevron.open {
  transform: rotate(0deg);
}

.target-details {
  padding: 0 1.2rem 1.2rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.details-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.thumb {
  width: 120px;
  height: 80px;
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid rgba(148, 163, 184, 0.25);
}

.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.actions {
  display: flex;
  gap: 0.5rem;
}

.exploit-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 0.6rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.exploit-card {
  border: 1px solid rgba(148, 163, 184, 0.15);
  border-radius: 10px;
  padding: 0.55rem 0.75rem;
  background: rgba(15, 23, 42, 0.6);
  display: flex;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.95rem;
}

.exploit-card input[type="checkbox"] {
  width: 18px;
  height: 18px;
  accent-color: #38bdf8;
  flex-shrink: 0;
}

.badge {
  background: rgba(56, 189, 248, 0.15);
  color: #38bdf8;
  border-radius: 999px;
  padding: 0.1rem 0.6rem;
  font-size: 0.75rem;
  font-weight: 500;
}

.empty {
  margin: 0;
  color: rgba(148, 163, 184, 0.75);
  font-size: 0.9rem;
}

@media (max-width: 720px) {
  .target-summary {
    grid-template-columns: 1fr auto;
    gap: 0.6rem;
  }
  .target-summary .status {
    justify-self: end;
  }
}
</style>
