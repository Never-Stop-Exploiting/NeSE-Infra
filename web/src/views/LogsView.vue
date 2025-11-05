<template>
  <section class="logs">
    <article class="panel filters">
      <h2>Log Filters</h2>
      <form class="form" @submit.prevent="applyFilters">
        <label>
          Level
          <select v-model="filters.level">
            <option value="">Any</option>
            <option value="debug">debug</option>
            <option value="info">info</option>
            <option value="warn">warn</option>
            <option value="error">error</option>
          </select>
        </label>
        <label>
          Source
          <input v-model="filters.source" placeholder="runner" />
        </label>
        <label>
          Run ID
          <input v-model="filters.runId" placeholder="run-uuid" />
        </label>
        <label>
          Flag ID
          <input v-model="filters.flagId" placeholder="flag-uuid" />
        </label>
        <label>
          Limit
          <input v-model.number="filters.limit" type="number" min="10" max="500" />
        </label>
        <button type="submit">Apply</button>
      </form>
    </article>

    <article class="panel results">
      <header>
        <h2>Log Stream</h2>
        <button class="refresh" @click="logStore.load">Refresh</button>
      </header>
      <div class="list">
        <article v-for="entry in logStore.logs" :key="entry.id" class="log-entry">
          <div class="meta">
            <span class="time">{{ entry.timestamp }}</span>
            <span class="level" :data-level="entry.level">{{ entry.level }}</span>
            <span class="source">{{ entry.source }}</span>
          </div>
          <p>{{ entry.message }}</p>
          <div class="tags">
            <span v-if="entry.runId">run={{ entry.runId }}</span>
            <span v-if="entry.flagId">flag={{ entry.flagId }}</span>
          </div>
        </article>
      </div>
    </article>
  </section>
</template>

<script setup lang="ts">
import { reactive, onMounted } from "vue";
import { useLogStore } from "@/stores/logs";

const logStore = useLogStore();
const filters = reactive({
  level: "",
  source: "",
  runId: "",
  flagId: "",
  limit: 100
});

function applyFilters() {
  logStore.setFilters({
    level: filters.level || undefined,
    source: filters.source || undefined,
    runId: filters.runId || undefined,
    flagId: filters.flagId || undefined,
    limit: filters.limit
  });
  logStore.load();
}

onMounted(() => {
  applyFilters();
});
</script>

<style scoped>
.logs {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 2fr);
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

.form {
  display: grid;
  gap: 0.75rem;
}

label {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

input,
select {
  padding: 0.6rem 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  background: rgba(15, 23, 42, 0.8);
  color: #f8fafc;
}

button {
  justify-self: start;
  background: #38bdf8;
  color: #0f172a;
  border: none;
  padding: 0.6rem 1.6rem;
  border-radius: 999px;
  font-weight: 600;
  cursor: pointer;
}

.results {
  max-height: calc(100vh - 200px);
}

.list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  overflow-y: auto;
  padding-right: 0.5rem;
}

.log-entry {
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 8px;
  padding: 0.75rem;
  background: rgba(15, 23, 42, 0.7);
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.meta {
  display: flex;
  gap: 0.75rem;
  font-size: 0.85rem;
  color: #94a3b8;
}

.level {
  text-transform: uppercase;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.2);
}

.level[data-level="error"] {
  background: rgba(248, 113, 113, 0.2);
  color: #f87171;
}

.level[data-level="warn"] {
  background: rgba(250, 204, 21, 0.2);
  color: #facc15;
}

.level[data-level="info"] {
  background: rgba(56, 189, 248, 0.2);
  color: #38bdf8;
}

.tags {
  display: flex;
  gap: 0.5rem;
  font-size: 0.75rem;
  color: #cbd5f5;
}

@media (max-width: 960px) {
  .logs {
    grid-template-columns: 1fr;
  }
}
</style>
