<template>
  <section class="layout">
    <article class="panel table">
      <header>
        <h2>Execution Queue</h2>
        <button class="refresh" :disabled="runStore.loading" @click="runStore.refresh">
          {{ runStore.loading ? "Refreshing..." : "Refresh" }}
        </button>
      </header>
      <div class="toolbar">
        <form class="filters" @submit.prevent="applyFilters">
          <div class="filter-group">
            <label>Status</label>
            <select v-model="filterDraft.status">
              <option value="">All</option>
              <option v-for="status in statusOptions" :key="status" :value="status">
                {{ status }}
              </option>
            </select>
          </div>
          <div class="filter-group">
            <label>Challenge</label>
            <select v-model="filterDraft.challengeId">
              <option value="">All</option>
              <option
                v-for="challenge in challengeStore.challenges"
                :key="challenge.id"
                :value="challenge.id"
              >
                {{ challenge.name }}
              </option>
            </select>
          </div>
          <div class="filter-group">
            <label>Exploit</label>
            <input v-model.trim="filterDraft.exploitName" type="text" placeholder="Exploit name" />
          </div>
          <div class="filter-group">
            <label>Target</label>
            <input v-model.trim="filterDraft.targetName" type="text" placeholder="Target or team" />
          </div>
          <div class="filter-actions">
            <button type="submit" class="ghost" :disabled="!filterDirty">Apply</button>
            <button
              type="button"
              class="ghost secondary"
              :disabled="!filterDirty && !hasActiveFilters"
              @click="resetFilters"
            >
              Reset
            </button>
          </div>
        </form>
        <div class="bulk-actions">
          <button
            type="button"
            class="ghost danger"
            :disabled="!canBulkCancel"
            @click="cancelSelectedRuns"
          >
            {{ bulkCanceling ? "Canceling..." : `Cancel Selected (${selectedRunsCount})` }}
          </button>
          <p v-if="bulkError" class="error-text">{{ bulkError }}</p>
        </div>
      </div>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th class="select-col">
                <input
                  type="checkbox"
                  :checked="allVisibleSelected"
                  :disabled="!runStore.runs.length"
                  @change="toggleSelectAll(($event.target as HTMLInputElement).checked)"
                />
              </th>
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
              <td class="select-col">
                <input
                  type="checkbox"
                  :checked="isSelected(run.id)"
                  @click.stop
                  @change.stop="toggleRowSelection(run.id, ($event.target as HTMLInputElement).checked)"
                />
              </td>
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
            class="ghost"
            type="button"
            :disabled="detailRefreshing"
            @click.stop="refreshSelectedRunDetail"
          >
            {{ detailRefreshing ? "Refreshing..." : "Refresh Detail" }}
          </button>
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
      <p v-if="detailRefreshError" class="error-text">{{ detailRefreshError }}</p>
      <section class="meta">
        <div>
          <strong>Exploit:</strong>
          <span>
            {{ runStore.selectedRun.exploitName ?? runStore.selectedRun.exploitId }}
            <small v-if="runStore.selectedRun.exploitName" class="meta-id">({{ runStore.selectedRun.exploitId }})</small>
          </span>
        </div>
        <div>
          <strong>Target:</strong>
          <span>
            {{ runStore.selectedRun.targetLabel ?? runStore.selectedRun.targetId }}
            <small v-if="runStore.selectedRun.targetLabel" class="meta-id">({{ runStore.selectedRun.targetId }})</small>
          </span>
        </div>
        <div><strong>Status:</strong> <span class="status" :data-status="runStore.selectedRun.status">{{ runStore.selectedRun.status }}</span></div>
        <div><strong>Runner:</strong> <span>{{ runStore.selectedRun.runnerId ?? "not assigned" }}</span></div>
        <div>
          <strong>Queued:</strong>
          <span>{{ formatUtcTimestamp(runStore.selectedRun.queuedAt) }}</span>
        </div>
        <div>
          <strong>Started:</strong>
          <span>{{ formatUtcTimestamp(runStore.selectedRun.startedAt) }}</span>
        </div>
        <div>
          <strong>Finished:</strong>
          <span>{{ formatUtcTimestamp(runStore.selectedRun.finishedAt) }}</span>
        </div>
        <div v-if="runStore.selectedRun.errorMessage"><strong>Error:</strong> <span>{{ runStore.selectedRun.errorMessage }}</span></div>
        <div v-if="showFlagDetail" class="flag-detail">
          <strong>Flag:</strong>
          <span class="flag-value">{{ runStore.selectedRun.flagValue }}</span>
          <small class="hint">Captured via {{ flagSourceLabel }}</small>
        </div>
        <div v-if="runStore.selectedRun.flagId" class="flag-panel">
          <button type="button" class="ghost" @click="toggleFlagPanel">
            {{ flagPanelOpen ? "Hide Flag Submission" : "View Flag Submission" }}
          </button>
          <div v-if="flagPanelOpen" class="flag-panel-body">
            <p v-if="flagLoading">Loading flag details…</p>
            <p v-else-if="flagError" class="error">{{ flagError }}</p>
            <div v-else-if="flagDetail">
              <div class="flag-status-row">
                <div>
                  <strong>Status:</strong>
                  <span class="flag-status" :data-status="flagDetail.status">{{ flagStatusText }}</span>
                  <small class="hint">Attempts: {{ flagDetail.submissionCount }}</small>
                </div>
                <button
                  type="button"
                  class="ghost danger"
                  :disabled="flagRetrying"
                  @click="retryFlagSubmission"
                >
                  {{ flagRetrying ? "Requeueing..." : "Retry Submission" }}
                </button>
              </div>
              <div class="attempts">
                <strong>Attempt History</strong>
                <ul class="attempt-list" v-if="flagAttempts.length">
                  <li v-for="attempt in flagAttempts" :key="attempt.id ?? attempt.submittedAt">
                    <div class="attempt-main">
                      <span>{{ formatTimestamp(attempt.submittedAt) || "Unknown time" }}</span>
                      <span class="code">HTTP {{ attempt.statusCode || 0 }}</span>
                    </div>
                    <div class="attempt-meta">
                      <span>Submitter: {{ attempt.submitterId ?? "unknown" }}</span>
                      <span v-if="attempt.error">Error: {{ attempt.error }}</span>
                      <span v-else>No error</span>
                    </div>
                  </li>
                </ul>
                <p v-else class="hint">No attempts recorded yet.</p>
              </div>
            </div>
          </div>
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
import { computed, onMounted, ref, watch } from "vue";
import { useRunStore, type RunFilters } from "@/stores/runs";
import { useChallengeStore } from "@/stores/challenges";
import type { FlagRecord } from "@/types";
import { fetchFlagDetail, retryFlag as apiRetryFlag } from "@/api/flags";
import { formatUtcTimestamp } from "@/utils/time";

const runStore = useRunStore();
const challengeStore = useChallengeStore();
const canceling = ref(false);
const detailRefreshing = ref(false);
const bulkCanceling = ref(false);
const bulkError = ref<string | null>(null);
const selectedRuns = ref<Set<string>>(new Set());
const statusOptions = ["pending", "running", "succeeded", "failed", "timeout", "canceled"];
const filterDraft = ref<RunFilters>(cloneFilters(runStore.filters as RunFilters));

const flagPanelOpen = ref(false);
const flagDetail = ref<FlagRecord | null>(null);
const flagLoading = ref(false);
const flagError = ref<string | null>(null);
const flagRetrying = ref(false);
const detailRefreshError = ref<string | null>(null);

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
  const source =
    flagDetail.value?.flagSource?.toLowerCase() ??
    runStore.selectedRun?.flagSource?.toLowerCase();
  if (source === "file") {
    return "flag file";
  }
  if (source === "stdout") {
    return "stdout";
  }
  return "unknown source";
});
const flagStatusText = computed(() => flagDetail.value?.status ?? "unknown");
const flagAttempts = computed(() => flagDetail.value?.attempts ?? []);

const hasActiveFilters = computed(() => {
  const value = runStore.filters as RunFilters;
  return Boolean(value.status || value.exploitName || value.targetName || value.challengeId);
});

const filterDirty = computed(
  () => !filtersEqual(filterDraft.value, runStore.filters as RunFilters)
);

const selectedRunsCount = computed(() => selectedRuns.value.size);
const allVisibleSelected = computed(
  () => runStore.runs.length > 0 && runStore.runs.every((run) => selectedRuns.value.has(run.id))
);
const canBulkCancel = computed(() => selectedRunsCount.value > 0 && !bulkCanceling.value);

watch(
  () => runStore.selectedRun?.id,
  () => {
    flagPanelOpen.value = false;
    flagDetail.value = null;
    flagError.value = null;
    flagRetrying.value = false;
    detailRefreshError.value = null;
    detailRefreshing.value = false;
  }
);

watch(
  () => runStore.runs.map((run) => run.id),
  (ids) => {
    const allowed = new Set(ids);
    selectedRuns.value = new Set([...selectedRuns.value].filter((id) => allowed.has(id)));
  }
);

watch(
  () => ({ ...(runStore.filters as RunFilters) }),
  (value) => {
    filterDraft.value = cloneFilters(value);
  },
  { deep: true }
);

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

async function cancelSelectedRuns() {
  if (!selectedRunsCount.value || bulkCanceling.value) {
    return;
  }
  bulkCanceling.value = true;
  bulkError.value = null;
  try {
    const { errors } = await runStore.cancelRunsBulk(Array.from(selectedRuns.value));
    if (errors.length) {
      bulkError.value = errors.join("; ");
    } else {
      selectedRuns.value = new Set();
    }
  } finally {
    bulkCanceling.value = false;
  }
}

async function applyFilters() {
  await runStore.setFilters(normalizeFilter(filterDraft.value));
  selectedRuns.value = new Set();
}

async function resetFilters() {
  filterDraft.value = { status: "", exploitName: "", targetName: "", challengeId: "" };
  await runStore.setFilters({});
  selectedRuns.value = new Set();
}

function toggleRowSelection(id: string, checked: boolean) {
  if (checked) {
    selectedRuns.value = new Set([...selectedRuns.value, id]);
  } else {
    selectedRuns.value = new Set([...selectedRuns.value].filter((value) => value !== id));
  }
}

function toggleSelectAll(checked: boolean) {
  if (checked) {
    selectedRuns.value = new Set(runStore.runs.map((run) => run.id));
  } else {
    selectedRuns.value = new Set();
  }
}

function isSelected(id: string) {
  return selectedRuns.value.has(id);
}

async function ensureFlagDetailLoaded() {
  if (!runStore.selectedRun?.flagId || flagDetail.value || flagLoading.value) {
    return;
  }
  flagLoading.value = true;
  flagError.value = null;
  try {
    flagDetail.value = await fetchFlagDetail(runStore.selectedRun.flagId);
  } catch (err) {
    console.error(err);
    flagError.value = "Failed to load flag details";
  } finally {
    flagLoading.value = false;
  }
}

function toggleFlagPanel() {
  if (!runStore.selectedRun?.flagId) {
    return;
  }
  flagPanelOpen.value = !flagPanelOpen.value;
  if (flagPanelOpen.value) {
    ensureFlagDetailLoaded();
  }
}

async function retryFlagSubmission() {
  if (!runStore.selectedRun?.flagId || flagRetrying.value) {
    return;
  }
  flagRetrying.value = true;
  try {
    await apiRetryFlag(runStore.selectedRun.flagId);
    flagDetail.value = null;
    await ensureFlagDetailLoaded();
  } catch (err) {
    console.error(err);
    flagError.value = "Failed to retry flag submission";
  } finally {
    flagRetrying.value = false;
  }
}

async function refreshSelectedRunDetail() {
  const currentId = runStore.selectedRun?.id;
  if (!currentId || detailRefreshing.value) {
    return;
  }
  detailRefreshing.value = true;
  detailRefreshError.value = null;
  try {
    await runStore.selectRun(currentId);
    if (flagPanelOpen.value) {
      flagDetail.value = null;
      flagError.value = null;
      await ensureFlagDetailLoaded();
    }
  } catch (err) {
    console.error(err);
    detailRefreshError.value = "Failed to refresh run detail";
  } finally {
    detailRefreshing.value = false;
  }
}

function formatTimestamp(value?: string) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function cloneFilters(source?: RunFilters): RunFilters {
  return {
    status: source?.status ?? "",
    exploitName: source?.exploitName ?? "",
    targetName: source?.targetName ?? "",
    challengeId: source?.challengeId ?? ""
  };
}

function normalizeFilter(source: RunFilters): RunFilters {
  const target: RunFilters = {};
  if (source.status?.trim()) {
    target.status = source.status.trim();
  }
  if (source.exploitName?.trim()) {
    target.exploitName = source.exploitName.trim();
  }
  if (source.targetName?.trim()) {
    target.targetName = source.targetName.trim();
  }
  if (source.challengeId?.trim()) {
    target.challengeId = source.challengeId.trim();
  }
  return target;
}

function filtersEqual(a: RunFilters, b: RunFilters) {
  const left = normalizeFilter(a);
  const right = normalizeFilter(b);
  return (
    (left.status ?? "") === (right.status ?? "") &&
    (left.exploitName ?? "") === (right.exploitName ?? "") &&
    (left.targetName ?? "") === (right.targetName ?? "") &&
    (left.challengeId ?? "") === (right.challengeId ?? "")
  );
}

onMounted(() => {
  challengeStore.load();
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

.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  justify-content: space-between;
  align-items: flex-start;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: flex-end;
}

.filter-group {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.filter-group label {
  font-size: 0.75rem;
  color: #94a3b8;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.filter-group input,
.filter-group select {
  background: rgba(15, 23, 42, 0.9);
  border: 1px solid rgba(148, 163, 184, 0.4);
  border-radius: 8px;
  padding: 0.4rem 0.6rem;
  color: #e2e8f0;
  min-width: 140px;
}

.filter-actions {
  display: flex;
  gap: 0.5rem;
}

.filters .ghost {
  padding: 0.35rem 0.9rem;
}

.filters .secondary {
  border-color: rgba(148, 163, 184, 0.5);
  color: #e2e8f0;
}

.bulk-actions {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  min-width: 220px;
}

.error-text {
  color: #fca5a5;
  font-size: 0.85rem;
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

.select-col {
  width: 2.5rem;
  text-align: center;
}

.select-col input {
  width: 1rem;
  height: 1rem;
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

.meta-id {
  margin-left: 0.35rem;
  color: rgba(148, 163, 184, 0.8);
  font-size: 0.75rem;
}

.flag-panel {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin-top: 0.5rem;
}

.flag-panel-body {
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 10px;
  padding: 0.75rem 1rem;
  background: rgba(15, 23, 42, 0.6);
}

.flag-status-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  margin-bottom: 0.5rem;
}

.flag-status {
  text-transform: capitalize;
  margin-left: 0.5rem;
}

.attempts {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.attempt-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.attempt-list li {
  border: 1px solid rgba(148, 163, 184, 0.15);
  border-radius: 8px;
  padding: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.attempt-main {
  display: flex;
  justify-content: space-between;
  font-size: 0.9rem;
}

.attempt-meta {
  font-size: 0.8rem;
  color: rgba(226, 232, 240, 0.8);
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.code {
  font-family: "JetBrains Mono", Menlo, monospace;
  font-size: 0.8rem;
}

.error {
  color: #f87171;
}

.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.35);
  color: #e2e8f0;
  padding: 0.4rem 0.9rem;
  border-radius: 999px;
  cursor: pointer;
}

.ghost.danger {
  border-color: rgba(248, 113, 113, 0.5);
  color: #f87171;
}

.ghost:disabled {
  opacity: 0.6;
  cursor: not-allowed;
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
