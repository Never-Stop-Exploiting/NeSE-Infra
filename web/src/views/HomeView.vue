<template>
  <section class="dashboard">
    <div class="header">
      <h2>Platform Snapshot</h2>
      <button class="refresh" @click="refreshAll" :disabled="busy">
        {{ busy ? "Refreshing..." : "Refresh" }}
      </button>
    </div>

    <div class="cards">
      <div class="card">
        <span class="label">Active Exploits</span>
        <strong>{{ exploitCount }}</strong>
      </div>
      <div class="card">
        <span class="label">Targets</span>
        <strong>{{ targetCount }}</strong>
      </div>
      <div class="card">
        <span class="label">Flags Awaiting Submit</span>
        <strong>{{ pendingFlags }}</strong>
      </div>
      <div class="card">
        <span class="label">Runs In Flight</span>
        <strong>{{ runningRuns }}</strong>
      </div>
    </div>

    <div class="panels">
      <article>
        <h3>Flag Submission Status</h3>
        <ul>
          <li v-for="status in flagStatuses" :key="status.label">
            <span>{{ status.label }}</span>
            <strong>{{ status.count }}</strong>
          </li>
        </ul>
      </article>
      <article>
        <h3>Runner Capacity</h3>
        <ul>
          <li v-for="runner in runnerStore.runners" :key="runner.id">
            <div>
              <strong>{{ runner.id }}</strong>
              <span class="badge" :data-status="runner.status">{{ runner.status }}</span>
            </div>
            <small>{{ runner.activeJobs }}/{{ runner.maxParallel }} active jobs</small>
          </li>
        </ul>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useExploitStore } from "@/stores/exploits";
import { useTargetStore } from "@/stores/targets";
import { useFlagStore } from "@/stores/flags";
import { useRunStore } from "@/stores/runs";
import { useRunnerStore } from "@/stores/runners";

const exploitStore = useExploitStore();
const targetStore = useTargetStore();
const flagStore = useFlagStore();
const runStore = useRunStore();
const runnerStore = useRunnerStore();

const busy = ref(false);

const exploitCount = computed(() => exploitStore.exploits.length);
const targetCount = computed(() => targetStore.targets.length);
const pendingFlags = computed(
  () => flagStore.flags.filter((flag) => flag.status !== "accepted").length
);
const runningRuns = computed(
  () => runStore.runs.filter((run) => run.status === "pending" || run.status === "running").length
);

const flagStatuses = computed(() => {
  const groups: Record<string, number> = {};
  for (const flag of flagStore.flags) {
    groups[flag.status] = (groups[flag.status] ?? 0) + 1;
  }
  return Object.entries(groups).map(([label, count]) => ({ label, count }));
});

async function refreshAll() {
  busy.value = true;
  await Promise.all([
    exploitStore.load(),
    targetStore.load(),
    flagStore.load(),
    runStore.refresh(),
    runnerStore.load()
  ]);
  busy.value = false;
}

onMounted(() => {
  refreshAll();
});
</script>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1rem 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.25);
}

.refresh {
  background: #38bdf8;
  color: #0f172a;
  border: none;
  border-radius: 999px;
  padding: 0.5rem 1.25rem;
  font-weight: 600;
  cursor: pointer;
}

.refresh:disabled {
  opacity: 0.6;
  cursor: wait;
}

.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 1rem;
}

.card {
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1.25rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.card strong {
  font-size: 1.75rem;
}

.label {
  color: #94a3b8;
  text-transform: uppercase;
  font-size: 0.75rem;
  letter-spacing: 0.08em;
}

.panels {
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
}

article {
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
}

article h3 {
  margin-top: 0;
}

article ul {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.badge {
  margin-left: 0.5rem;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  font-size: 0.75rem;
  text-transform: capitalize;
  background: rgba(148, 163, 184, 0.2);
}

.badge[data-status="online"] {
  color: #22c55e;
}

.badge[data-status="degraded"] {
  color: #f97316;
}

.badge[data-status="offline"] {
  color: #ef4444;
}

article li {
  display: flex;
  flex-direction: column;
}

article li div {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

article li small {
  color: #94a3b8;
}
</style>
