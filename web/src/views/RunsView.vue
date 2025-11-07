<template>
  <section class="layout">
    <article class="panel table">
      <header>
        <h2>Execution Queue</h2>
        <button class="refresh" :disabled="runStore.loading" @click="runStore.refresh">
          {{ runStore.loading ? "Refreshing..." : "Refresh" }}
        </button>
      </header>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Exploit</th>
              <th>Target</th>
              <th>Status</th>
              <th>Runner</th>
              <th>Queued</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="run in runStore.runs"
              :key="run.id"
              :class="{ active: runStore.selectedRun?.id === run.id }"
              @click="select(run.id)"
            >
              <td>
                <span class="mono truncate" :title="run.id">{{ run.id }}</span>
              </td>
              <td>
                <span class="truncate" :title="run.exploitName ?? run.exploitId">
                  {{ run.exploitName ?? run.exploitId }}
                </span>
              </td>
              <td>
                <span class="truncate" :title="run.targetLabel ?? run.targetId">
                  {{ run.targetLabel ?? run.targetId }}
                </span>
              </td>
              <td><span class="status" :data-status="run.status">{{ run.status }}</span></td>
              <td>
                <span class="mono truncate" :title="run.runnerId ?? 'pending'">
                  {{ run.runnerId ?? "pending" }}
                </span>
              </td>
              <td><span class="mono truncate" :title="run.queuedAt">{{ run.queuedAt }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="table-actions">
        <button
          v-if="runStore.hasMore"
          class="load-more"
          type="button"
          :disabled="runStore.loading"
          @click="runStore.loadMore"
        >
          {{ runStore.loading ? "Loading..." : "Load More" }}
        </button>
      </div>
    </article>

    <article v-if="runStore.selectedRun" class="panel detail">
      <header>
        <h3>Run Detail</h3>
        <div class="detail-actions">
          <button
            v-if="showCancelButton"
            class="danger"
            type="button"
            :disabled="canceling"
            @click.stop="cancelSelectedRun"
          >
            {{ canceling ? "Processing..." : cancelActionLabel }}
          </button>
          <button class="close" type="button" @click="runStore.clearSelected">Close</button>
        </div>
      </header>
      <section class="meta">
        <div>
          <strong>Exploit:</strong> <span>{{ runStore.selectedRun.exploitName ?? runStore.selectedRun.exploitId }}</span>
        </div>
        <div><strong>Target:</strong> <span>{{ runStore.selectedRun.targetLabel ?? runStore.selectedRun.targetId }}</span></div>
        <div><strong>Status:</strong> <span class="status" :data-status="runStore.selectedRun.status">{{ runStore.selectedRun.status }}</span></div>
        <div><strong>Runner:</strong> <span>{{ runStore.selectedRun.runnerId ?? "not assigned" }}</span></div>
        <div><strong>Started:</strong> <span>{{ runStore.selectedRun.startedAt ?? "-" }}</span></div>
        <div><strong>Finished:</strong> <span>{{ runStore.selectedRun.finishedAt ?? "-" }}</span></div>
        <div v-if="runStore.selectedRun.errorMessage"><strong>Error:</strong> <span>{{ runStore.selectedRun.errorMessage }}</span></div>
        <div v-if="showFlagDetail" class="flag-detail">
          <strong>Flag:</strong>
          <span class="flag-value">{{ runStore.selectedRun.flagValue }}</span>
          <small class="hint">Captured via {{ flagSourceLabel }}</small>
        </div>
      </section>
      <section class="stdout">
        <h4>STDOUT</h4>
        <pre>{{ runStore.selectedRun.stdout ?? "No stdout captured" }}</pre>
      </section>
      <section class="stderr">
        <h4>STDERR</h4>
        <pre>{{ runStore.selectedRun.stderr ?? "No stderr" }}</pre>
      </section>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRunStore } from "@/stores/runs";

const runStore = useRunStore();
const canceling = ref(false);

const cancelActionLabel = computed(() => {
  const status = runStore.selectedRun?.status;
  if (status === "running") {
    return "Terminate Run";
  }
  if (status === "pending") {
    return "Remove From Queue";
  }
  return "";
});

const showCancelButton = computed(() => cancelActionLabel.value.length > 0);
const showFlagDetail = computed(
  () =>
    !!(
      runStore.selectedRun &&
      runStore.selectedRun.flagValue &&
      runStore.selectedRun.status === "succeeded"
    )
);
const flagSourceLabel = computed(() => {
  const source = runStore.selectedRun?.flagSource?.toLowerCase();
  if (source === "file") {
    return "flag file";
  }
  if (source === "stdout") {
    return "stdout";
  }
  return "unknown source";
});

function select(id: string) {
  runStore.selectRun(id);
}

async function cancelSelectedRun() {
  if (!runStore.selectedRun || canceling.value) {
    return;
  }
  canceling.value = true;
  try {
    await runStore.cancelRun(runStore.selectedRun.id);
  } finally {
    canceling.value = false;
  }
}

onMounted(() => {
  runStore.refresh();
});
</script>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1.5fr) minmax(0, 1fr);
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

.table-actions {
  display: flex;
  justify-content: center;
  margin-top: 0.75rem;
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
  font-size: 0.72rem;
  letter-spacing: 0.05em;
  white-space: nowrap;
}

tr.active {
  background: rgba(56, 189, 248, 0.08);
}

.status {
  text-transform: capitalize;
  padding: 0.1rem 0.5rem;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.2);
}

.status[data-status="succeeded"] {
  background: rgba(34, 197, 94, 0.2);
  color: #4ade80;
}

.status[data-status="failed"],
.status[data-status="timeout"] {
  background: rgba(248, 113, 113, 0.2);
  color: #f87171;
}

.status[data-status="running"],
.status[data-status="pending"] {
  background: rgba(250, 204, 21, 0.2);
  color: #facc15;
}

.status[data-status="canceled"] {
  background: rgba(148, 163, 184, 0.3);
  color: #cbd5f5;
}

.truncate {
  display: inline-block;
  max-width: 16ch;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: bottom;
}

.mono {
  font-family: "JetBrains Mono", Menlo, monospace;
  font-size: 0.82rem;
}

.detail header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.detail-actions {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}

.danger {
  background: #f87171;
  color: #0f172a;
  border: none;
  border-radius: 999px;
  padding: 0.4rem 0.9rem;
  font-weight: 600;
  cursor: pointer;
}

.danger:disabled {
  opacity: 0.6;
  cursor: wait;
}

.flag-detail {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.flag-value {
  font-family: "JetBrains Mono", Menlo, monospace;
  font-size: 0.9rem;
  color: #facc15;
}

.hint {
  color: rgba(148, 163, 184, 0.8);
  font-size: 0.8rem;
}

.close,
.refresh,
.load-more {
  background: rgba(148, 163, 184, 0.2);
  border: none;
  color: #e2e8f0;
  padding: 0.4rem 0.9rem;
  border-radius: 999px;
  cursor: pointer;
}

.load-more[disabled],
.refresh[disabled] {
  opacity: 0.6;
  cursor: not-allowed;
}

.meta {
  display: grid;
  gap: 0.4rem;
  font-size: 0.9rem;
  color: #cbd5f5;
}

pre {
  background: rgba(15, 23, 42, 0.9);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 8px;
  padding: 0.75rem;
  max-height: 240px;
  overflow: auto;
  color: #e2e8f0;
  font-family: "JetBrains Mono", Menlo, monospace;
}

@media (max-width: 960px) {
  .layout {
    grid-template-columns: 1fr;
  }
}
</style>
