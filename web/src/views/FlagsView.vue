<template>
  <section class="layout">
    <article class="panel table">
      <header>
        <h2>Flags</h2>
        <button class="refresh" @click="flagStore.load">Refresh</button>
      </header>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>Flag</th>
              <th>Status</th>
              <th>Attempts</th>
              <th>Challenge</th>
              <th>Target</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="flag in flagStore.flags"
              :key="flag.id"
              :class="{ active: selectedFlag?.id === flag.id }"
              @click="selectFlag(flag.id)"
            >
              <td><code>{{ flag.value }}</code></td>
              <td><span class="status" :data-status="flag.status">{{ flag.status }}</span></td>
              <td>{{ flag.submissionCount }}</td>
              <td>{{ flag.challengeId }}</td>
              <td>{{ flag.targetId }}</td>
              <td>
                <button class="retry" @click.stop="retry(flag.id)">Retry</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </article>

    <article class="panel detail" v-if="selectedFlag">
      <header>
        <h3>Flag Detail</h3>
        <span>Status: {{ selectedFlag.status }}</span>
      </header>
      <section>
        <h4>Submission Attempts</h4>
        <ul>
          <li v-for="attempt in selectedFlag.attempts" :key="attempt.id">
            <div>
              <strong>{{ attempt.submitterId }}</strong>
              <span>#{{ attempt.statusCode }}</span>
            </div>
            <small>{{ attempt.submittedAt }}</small>
            <p v-if="attempt.error">{{ attempt.error }}</p>
          </li>
        </ul>
      </section>
      <section>
        <h4>Logs</h4>
        <textarea readonly rows="10">{{ logOutput }}</textarea>
      </section>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useFlagStore } from "@/stores/flags";

const flagStore = useFlagStore();
const selectedFlagId = ref<string | null>(null);
const logOutput = ref("Select a flag to view logs");

const selectedFlag = computed(() =>
  flagStore.flags.find((flag) => flag.id === selectedFlagId.value)
);

async function selectFlag(id: string) {
  selectedFlagId.value = id;
  const logs = await flagStore.loadLogs(id);
  logOutput.value = logs.join("\n");
}

async function retry(id: string) {
  await flagStore.retry(id);
  await flagStore.load();
}

onMounted(() => {
  flagStore.load();
});
</script>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
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

.table-wrapper {
  overflow-x: auto;
}

table {
  width: 100%;
  border-collapse: collapse;
}

th,
td {
  padding: 0.75rem;
  border-bottom: 1px solid rgba(148, 163, 184, 0.15);
}

th {
  text-align: left;
  color: #94a3b8;
  text-transform: uppercase;
  font-size: 0.75rem;
  letter-spacing: 0.05em;
}

tr.active {
  background: rgba(56, 189, 248, 0.1);
}

.refresh {
  background: rgba(148, 163, 184, 0.2);
  border: none;
  color: #e2e8f0;
  padding: 0.4rem 1rem;
  border-radius: 999px;
  cursor: pointer;
}

.retry {
  background: transparent;
  border: 1px solid rgba(56, 189, 248, 0.6);
  color: #38bdf8;
  border-radius: 999px;
  padding: 0.25rem 0.9rem;
  cursor: pointer;
}

.status {
  text-transform: capitalize;
  padding: 0.2rem 0.6rem;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.2);
}

.status[data-status="accepted"] {
  background: rgba(34, 197, 94, 0.2);
  color: #4ade80;
}

.status[data-status="retry"],
.status[data-status="submitting"] {
  background: rgba(250, 204, 21, 0.2);
  color: #facc15;
}

.status[data-status="rejected"],
.status[data-status="dropped"] {
  background: rgba(248, 113, 113, 0.2);
  color: #f87171;
}

.detail textarea {
  width: 100%;
  padding: 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  background: rgba(15, 23, 42, 0.9);
  color: #e2e8f0;
  font-family: "JetBrains Mono", Menlo, monospace;
  resize: vertical;
}

.detail ul {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.detail li {
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 8px;
  padding: 0.75rem;
  background: rgba(15, 23, 42, 0.6);
}

.detail li div {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.detail small {
  color: #94a3b8;
}

.detail p {
  margin: 0.25rem 0 0;
}

@media (max-width: 960px) {
  .layout {
    grid-template-columns: 1fr;
  }
}
</style>
