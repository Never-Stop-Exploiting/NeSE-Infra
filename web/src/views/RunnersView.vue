<template>
  <section class="panel">
    <header>
      <div>
        <h2>Runner Fleet</h2>
        <p class="hint">Overview of registered agents and their current workload.</p>
      </div>
      <button class="refresh" @click="runnerStore.load">Refresh</button>
    </header>
    <div class="cards">
      <article v-for="runner in runnerStore.runners" :key="runner.id" class="card">
        <div class="header">
          <h3>{{ runner.id }}</h3>
          <span class="status" :data-status="runner.status">{{ runner.status }}</span>
        </div>
        <div class="meta">
          <span>Type: {{ runner.type }}</span>
          <span>Hostname: {{ runner.hostname ?? "-" }}</span>
          <span>Active Jobs: {{ runner.activeJobs }}/{{ runner.maxParallel }}</span>
          <span>Last heartbeat: {{ runner.lastHeartbeat ?? "-" }}</span>
          <span>Source IP: {{ runner.lastIp ?? "-" }}</span>
          <span>
            Broker:
            <span class="broker-badge" :class="brokerBadgeClass(runner.labels?.broker)">
              {{ brokerBadgeText(runner.labels?.broker) }}
            </span>
          </span>
        </div>
        <div v-if="runner.tags?.length" class="tags">
          <span v-for="tag in runner.tags" :key="tag">{{ tag }}</span>
        </div>
        <div v-if="runner.config && Object.keys(runner.config).length" class="config">
          <span v-for="(value, key) in runner.config" :key="key">{{ key }}={{ value }}</span>
        </div>
        <div v-if="runner.labels && Object.keys(runner.labels).length" class="labels">
          <span v-for="(value, key) in runner.labels" :key="key">{{ key }}={{ value }}</span>
        </div>
        <button
          v-if="runner.status === 'offline'"
          class="danger"
          type="button"
          @click="handleDelete(runner.id)"
        >
          Delete
        </button>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted } from "vue";
import { useRunnerStore } from "@/stores/runners";

const runnerStore = useRunnerStore();

function brokerBadgeText(status?: string) {
  if (!status) return "Unknown";
  if (status === "connected") return "Connected";
  if (status === "disconnected") return "Disconnected";
  return status;
}

function brokerBadgeClass(status?: string) {
  if (status === "connected") return "connected";
  if (status === "disconnected") return "disconnected";
  return "unknown";
}

async function handleDelete(id: string) {
  if (!confirm("Remove this offline runner?")) return;
  await runnerStore.remove(id);
}

onMounted(() => {
  runnerStore.load();
});
</script>

<style scoped>
.panel {
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.25);
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.hint {
  margin: 0;
  color: #94a3b8;
}

.refresh {
  background: rgba(148, 163, 184, 0.2);
  border: none;
  color: #e2e8f0;
  padding: 0.4rem 1rem;
  border-radius: 999px;
  cursor: pointer;
}

.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 1rem;
}

.card {
  background: rgba(15, 23, 42, 0.7);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 12px;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.status {
  text-transform: capitalize;
  padding: 0.2rem 0.6rem;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.2);
}

.status[data-status="online"] {
  color: #22c55e;
}

.status[data-status="degraded"] {
  color: #f97316;
}

.status[data-status="offline"] {
  color: #ef4444;
}

.meta {
  display: grid;
  gap: 0.25rem;
  color: #cbd5f5;
  font-size: 0.9rem;
}
.meta span {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
}

.labels {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.config {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.tags span {
  background: rgba(34, 197, 94, 0.15);
  border-radius: 999px;
  padding: 0.2rem 0.6rem;
  font-size: 0.75rem;
  color: #4ade80;
}

.labels span {
  background: rgba(56, 189, 248, 0.15);
  border-radius: 999px;
  padding: 0.2rem 0.6rem;
  font-size: 0.75rem;
  color: #38bdf8;
}

.config span {
  background: rgba(234, 179, 8, 0.15);
  border-radius: 999px;
  padding: 0.2rem 0.6rem;
  font-size: 0.75rem;
  color: #facc15;
}

.broker-badge {
  border-radius: 999px;
  padding: 0.15rem 0.55rem;
  font-size: 0.75rem;
  background: rgba(148, 163, 184, 0.15);
  color: #e2e8f0;
  text-transform: capitalize;
}

.broker-badge.connected {
  background: rgba(74, 222, 128, 0.18);
  color: #4ade80;
}

.broker-badge.disconnected {
  background: rgba(248, 113, 113, 0.18);
  color: #f87171;
}

.broker-badge.unknown {
  background: rgba(148, 163, 184, 0.18);
  color: #cbd5f5;
}

.danger {
  align-self: flex-start;
  background: rgba(248, 113, 113, 0.2);
  border: 1px solid rgba(248, 113, 113, 0.4);
  color: #f87171;
  padding: 0.3rem 0.8rem;
  border-radius: 8px;
  cursor: pointer;
}
</style>
