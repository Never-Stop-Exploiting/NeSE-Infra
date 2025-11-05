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
        </div>
        <div v-if="runner.labels && Object.keys(runner.labels).length" class="labels">
          <span v-for="(value, key) in runner.labels" :key="key">{{ key }}={{ value }}</span>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted } from "vue";
import { useRunnerStore } from "@/stores/runners";

const runnerStore = useRunnerStore();

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

.labels {
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
}

.labels span {
  background: rgba(56, 189, 248, 0.15);
  border-radius: 999px;
  padding: 0.2rem 0.6rem;
  font-size: 0.75rem;
  color: #38bdf8;
}
</style>
