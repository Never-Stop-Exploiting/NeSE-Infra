<template>
  <section class="layout">
    <article class="panel table">
      <header class="panel-header">
        <h2>Flags</h2>
        <button class="refresh" :disabled="flagStore.loading" @click="flagStore.refresh">
          {{ flagStore.loading ? "Refreshing..." : "Refresh" }}
        </button>
      </header>
      <div class="toolbar">
        <form class="filters" @submit.prevent="applyFilters">
          <div class="filter-group">
            <label>Status</label>
            <select v-model="filterDraft.status">
              <option v-for="option in statusOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </div>
          <div class="filter-group">
            <label>Exploit</label>
            <input
              v-model.trim="filterDraft.challengeName"
              type="text"
              placeholder="Exploit name"
            />
          </div>
          <div class="filter-group">
            <label>Team</label>
            <input
              v-model.trim="filterDraft.targetName"
              type="text"
              placeholder="Target or team"
            />
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
            class="ghost danger"
            type="button"
            :disabled="!canBulkCancel"
            @click="cancelSelectedFlags"
          >
            {{ bulkCanceling ? "Canceling..." : `Cancel Selected (${selectedCount})` }}
          </button>
          <button
            class="ghost"
            type="button"
            :disabled="!canBulkRetry"
            @click="retrySelectedFlags"
          >
            {{ bulkRetrying ? "Retrying..." : `Retry Selected (${selectedCount})` }}
          </button>
        </div>
      </div>
      <p v-if="bulkError" class="error-text">{{ bulkError }}</p>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th class="select-col">
                <input
                  type="checkbox"
                  :checked="allVisibleSelected"
                  :disabled="!flagStore.flags.length"
                  @change="toggleSelectAll(($event.target as HTMLInputElement).checked)"
                />
              </th>
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
              <td class="select-col">
                <input
                  type="checkbox"
                  :checked="isSelected(flag.id)"
                  :disabled="!canCancelFlag(flag)"
                  @click.stop
                  @change.stop="toggleFlagSelection(flag, ($event.target as HTMLInputElement).checked)"
                />
              </td>
              <td>
                <code :title="flag.value">{{ truncateFlag(flag.value) }}</code>
              </td>
              <td><span class="status" :data-status="flag.status">{{ formatStatus(flag.status) }}</span></td>
              <td>{{ flag.submissionCount }}</td>
              <td>{{ flag.challengeName || flag.challengeId || "—" }}</td>
              <td>{{ flag.targetLabel || flag.targetId || "—" }}</td>
              <td>
                <button
                  class="cancel"
                  :disabled="!canCancelFlag(flag) || cancelingFlag === flag.id"
                  @click.stop="cancelFlagAction(flag.id)"
                >
                  {{ cancelingFlag === flag.id ? "Canceling..." : "Cancel" }}
                </button>
                <button
                  class="retry"
                  :disabled="retryingFlag === flag.id"
                  @click.stop="retry(flag.id)"
                >
                  {{ retryingFlag === flag.id ? "Retrying..." : "Retry" }}
                </button>
              </td>
            </tr>
            <tr v-if="!flagStore.flags.length && !flagStore.loading">
              <td colspan="7" class="empty">No flags found</td>
            </tr>
          </tbody>
        </table>
        <div class="table-actions">
          <button
            class="load-more"
            type="button"
            :disabled="flagStore.loading || !flagStore.hasMore"
            @click="flagStore.loadMore"
          >
            {{
              flagStore.loading ? "Loading..." : flagStore.hasMore ? "Load More" : "No More Results"
            }}
          </button>
        </div>
      </div>
    </article>

    <article class="panel detail" v-if="selectedFlag">
      <header>
        <h3>Flag Detail</h3>
        <span>Status: {{ formatStatus(selectedFlag.status) }}</span>
        <button
          class="refresh"
          type="button"
          :disabled="detailLoading || !selectedFlagId"
          @click="refreshSelectedFlagDetail"
        >
          {{ detailLoading ? "Refreshing..." : "Refresh Detail" }}
        </button>
      </header>
      <section class="detail-meta">
        <div>
          <strong>Flag ID:</strong>
          <span>{{ selectedFlagDetail?.id ?? selectedFlag.id }}</span>
        </div>
        <div>
          <strong>Challenge:</strong>
          <span>
            {{ selectedFlagDetail?.challengeName ?? selectedFlag.challengeName ?? "Unknown" }}
            <small class="meta-id">({{ selectedFlagDetail?.challengeId ?? selectedFlag.challengeId }})</small>
          </span>
        </div>
        <div>
          <strong>Target:</strong>
          <span>
            {{ selectedFlagDetail?.targetLabel ?? selectedFlag.targetLabel ?? "Unknown" }}
            <small class="meta-id">({{ selectedFlagDetail?.targetId ?? selectedFlag.targetId }})</small>
          </span>
        </div>
        <div>
          <strong>Run ID:</strong>
          <span>{{ selectedFlagDetail?.runId ?? selectedFlag.runId ?? "—" }}</span>
        </div>
        <div>
          <strong>Created:</strong>
          <span>{{ formatTimestamp(selectedFlagDetail?.createdAt ?? selectedFlag.createdAt) }}</span>
        </div>
        <div>
          <strong>Last Submitted:</strong>
          <span>{{ formatTimestamp(selectedFlagDetail?.lastSubmittedAt ?? selectedFlag.lastSubmittedAt) }}</span>
        </div>
      </section>
      <section>
        <h4>Flag Value</h4>
        <pre class="flag-value">{{ selectedFlagDetail?.value ?? selectedFlag.value }}</pre>
      </section>
      <section>
        <h4>Submission Attempts</h4>
        <p v-if="detailLoading">Loading attempts…</p>
        <p v-else-if="detailError" class="error">{{ detailError }}</p>
        <ul v-else>
          <li
            v-for="attempt in selectedFlagDetail?.attempts ?? []"
            :key="attempt.id ?? attempt.submittedAt"
          >
            <div>
              <strong>{{ attempt.submitterId ?? "unknown" }}</strong>
              <span>#{{ attempt.statusCode ?? 0 }}</span>
            </div>
            <small>{{ formatTimestamp(attempt.submittedAt) }}</small>
            <p v-if="attempt.error">{{ attempt.error }}</p>
          </li>
          <li v-if="!selectedFlagDetail?.attempts?.length" class="hint">No attempts recorded.</li>
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
import { computed, onMounted, ref, watch } from "vue";
import { useFlagStore, type FlagFilters } from "@/stores/flags";
import type { FlagRecord } from "@/types";
import { fetchFlagDetail } from "@/api/flags";

const flagStore = useFlagStore();
const selectedFlagId = ref<string | null>(null);
const selectedFlagDetail = ref<FlagRecord | null>(null);
const detailLoading = ref(false);
const detailError = ref<string | null>(null);
const logOutput = ref("Select a flag to view logs");
const retryingFlag = ref<string | null>(null);
const selectedFlags = ref<Set<string>>(new Set());
const cancelingFlag = ref<string | null>(null);
const bulkCanceling = ref(false);
const bulkRetrying = ref(false);
const bulkError = ref<string | null>(null);
const filterDraft = ref<FlagFilters>(cloneFilters(flagStore.filters as FlagFilters));

const selectedFlag = computed(() =>
  flagStore.flags.find((flag) => flag.id === selectedFlagId.value)
);

const statusOptions = [
  { label: "All", value: "" },
  { label: "New", value: "new" },
  { label: "Submitting", value: "submitting" },
  { label: "Accepted", value: "accepted" },
  { label: "Failed", value: "failed" },
  { label: "Duplicated", value: "duplicated" },
  { label: "Retry", value: "retry" },
  { label: "Dropped", value: "dropped" },
];

const statusTextMap: Record<string, string> = {
  new: "New",
  submitting: "Submitting",
  accepted: "Accepted",
  failed: "Failed",
  duplicated: "Duplicated",
  retry: "Retry",
  dropped: "Dropped"
};

const selectedCount = computed(() => selectedFlags.value.size);
const hasActiveFilters = computed(() => {
  const value = flagStore.filters as FlagFilters;
  return Boolean(value.status || value.challengeName || value.targetName);
});
const filterDirty = computed(
  () => !filtersEqual(filterDraft.value, flagStore.filters as FlagFilters)
);
const allVisibleSelected = computed(() => {
  const cancellable = flagStore.flags.filter((flag) => canCancelFlag(flag));
  if (!cancellable.length) {
    return false;
  }
  return cancellable.every((flag) => selectedFlags.value.has(flag.id));
});
const canBulkCancel = computed(
  () => selectedCount.value > 0 && !bulkCanceling.value && !bulkRetrying.value
);
const canBulkRetry = computed(
  () => selectedCount.value > 0 && !bulkRetrying.value && !bulkCanceling.value
);

watch(
  () => flagStore.flags.map((flag) => `${flag.id}:${flag.status}`),
  () => {
    const allowed = new Set(flagStore.flags.filter((flag) => canCancelFlag(flag)).map((flag) => flag.id));
    selectedFlags.value = new Set([...selectedFlags.value].filter((id) => allowed.has(id)));
  }
);

watch(
  () => ({ ...(flagStore.filters as FlagFilters) }),
  (value) => {
    filterDraft.value = cloneFilters(value);
  },
  { deep: true }
);

async function selectFlag(id: string) {
  if (selectedFlagId.value === id && selectedFlagDetail.value) {
    return;
  }
  selectedFlagId.value = id;
  await loadFlagDetail(id);
}

async function loadFlagDetail(id: string) {
  detailLoading.value = true;
  detailError.value = null;
  selectedFlagDetail.value = null;
  logOutput.value = "Loading logs…";
  try {
    const detail = await fetchFlagDetail(id);
    if (detail) {
      selectedFlagDetail.value = detail;
    }
    const logs = await flagStore.loadLogs(id);
    logOutput.value = logs.length ? logs.join("\n") : "No logs recorded.";
  } catch (err) {
    console.error(err);
    detailError.value = "Failed to load flag detail";
    logOutput.value = "Failed to load logs.";
  } finally {
    detailLoading.value = false;
  }
}

async function retry(id: string) {
  if (retryingFlag.value) {
    return;
  }
  retryingFlag.value = id;
  try {
    await flagStore.retry(id);
    const next = new Set(selectedFlags.value);
    next.delete(id);
    selectedFlags.value = next;
    if (selectedFlagId.value === id) {
      await loadFlagDetail(id);
    }
  } finally {
    retryingFlag.value = null;
  }
}

function truncateFlag(value: string, length = 24) {
  if (!value) return "";
  return value.length > length ? `${value.slice(0, length)}…` : value;
}

function isSelected(id: string) {
  return selectedFlags.value.has(id);
}

function toggleFlagSelection(flag: FlagRecord, checked: boolean) {
  if (!canCancelFlag(flag)) {
    return;
  }
  const next = new Set(selectedFlags.value);
  if (checked) {
    next.add(flag.id);
  } else {
    next.delete(flag.id);
  }
  selectedFlags.value = next;
}

function toggleSelectAll(checked: boolean) {
  if (!checked) {
    selectedFlags.value = new Set();
    return;
  }
  const ids = flagStore.flags.filter((flag) => canCancelFlag(flag)).map((flag) => flag.id);
  selectedFlags.value = new Set(ids);
}

function canCancelFlag(flag: FlagRecord) {
  return !["accepted", "dropped", "failed", "duplicated"].includes(flag.status);
}

async function applyFilters() {
  await flagStore.setFilters(normalizeFilter(filterDraft.value));
  selectedFlagId.value = null;
  selectedFlagDetail.value = null;
  logOutput.value = "Select a flag to view logs";
  selectedFlags.value = new Set();
  bulkError.value = null;
}

async function resetFilters() {
  filterDraft.value = { status: "", challengeName: "", targetName: "" };
  await flagStore.setFilters({});
  selectedFlagId.value = null;
  selectedFlagDetail.value = null;
  logOutput.value = "Select a flag to view logs";
  selectedFlags.value = new Set();
  bulkError.value = null;
}

async function cancelFlagAction(id: string) {
  if (cancelingFlag.value) {
    return;
  }
  cancelingFlag.value = id;
  try {
    await flagStore.cancel(id);
    const next = new Set(selectedFlags.value);
    next.delete(id);
    selectedFlags.value = next;
    if (selectedFlagId.value === id) {
      await loadFlagDetail(id);
    }
  } finally {
    cancelingFlag.value = null;
  }
}

async function cancelSelectedFlags() {
  if (!selectedCount.value || bulkCanceling.value) {
    return;
  }
  bulkCanceling.value = true;
  bulkError.value = null;
  try {
    const response = await flagStore.cancelSelected(Array.from(selectedFlags.value));
    handleBulkResponse(response.results, "cancel");
  } finally {
    bulkCanceling.value = false;
  }
}

async function retrySelectedFlags() {
  if (!selectedCount.value || bulkRetrying.value) {
    return;
  }
  bulkRetrying.value = true;
  bulkError.value = null;
  try {
    const response = await flagStore.retrySelected(Array.from(selectedFlags.value));
    handleBulkResponse(response.results, "retry");
  } finally {
    bulkRetrying.value = false;
  }
}

function handleBulkResponse(
  results: Array<{ flagId?: string; error?: string }> | undefined,
  action: string
) {
  const errors = (results ?? [])
    .filter((result) => result.error)
    .map((result) => `${result.flagId ?? "unknown"}: ${result.error}`);
  bulkError.value = errors.length ? `Failed to ${action}: ${errors.join("; ")}` : null;
  selectedFlags.value = new Set();
}

function formatTimestamp(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function formatStatus(value?: string) {
  if (!value) {
    return "Unknown";
  }
  return statusTextMap[value] ?? value.charAt(0).toUpperCase() + value.slice(1);
}

async function refreshSelectedFlagDetail() {
  if (!selectedFlagId.value) {
    return;
  }
  await loadFlagDetail(selectedFlagId.value);
}

function cloneFilters(source?: FlagFilters): FlagFilters {
  return {
    status: source?.status ?? "",
    challengeName: source?.challengeName ?? "",
    targetName: source?.targetName ?? ""
  };
}

function normalizeFilter(source: FlagFilters): FlagFilters {
  const target: FlagFilters = {};
  if (source.status?.trim()) {
    target.status = source.status.trim();
  }
  if (source.challengeName?.trim()) {
    target.challengeName = source.challengeName.trim();
  }
  if (source.targetName?.trim()) {
    target.targetName = source.targetName.trim();
  }
  return target;
}

function filtersEqual(a: FlagFilters, b: FlagFilters) {
  const left = normalizeFilter(a);
  const right = normalizeFilter(b);
  return (
    (left.status ?? "") === (right.status ?? "") &&
    (left.challengeName ?? "") === (right.challengeName ?? "") &&
    (left.targetName ?? "") === (right.targetName ?? "")
  );
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

.panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
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

.filters input,
.filters select {
  background: rgba(15, 23, 42, 0.85);
  color: #f8fafc;
  border: 1px solid rgba(148, 163, 184, 0.3);
  border-radius: 999px;
  padding: 0.35rem 0.9rem;
}

.filters input {
  border-radius: 8px;
  min-width: 220px;
}

.filter-actions {
  display: flex;
  gap: 0.35rem;
}

.bulk-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  align-items: center;
}

.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.5);
  color: #e2e8f0;
  border-radius: 999px;
  padding: 0.35rem 0.9rem;
  cursor: pointer;
}

.ghost.danger {
  border-color: rgba(248, 113, 113, 0.8);
  color: #fecaca;
}

.ghost.secondary {
  border-color: rgba(148, 163, 184, 0.35);
  color: rgba(226, 232, 240, 0.8);
}

.ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.error-text {
  color: #fca5a5;
  font-size: 0.85rem;
  margin-top: 0.35rem;
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

.select-col {
  width: 2.5rem;
  text-align: center;
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

.cancel {
  background: transparent;
  border: 1px solid rgba(248, 113, 113, 0.6);
  color: #fecaca;
  border-radius: 999px;
  padding: 0.25rem 0.9rem;
  cursor: pointer;
  margin-right: 0.4rem;
}

.table-actions {
  margin-top: 0.75rem;
  display: flex;
  justify-content: center;
}

.load-more {
  background: rgba(148, 163, 184, 0.15);
  border: 1px solid rgba(148, 163, 184, 0.3);
  color: #e2e8f0;
  padding: 0.4rem 1.2rem;
  border-radius: 999px;
}

.retry:disabled,
.cancel:disabled,
.actions .ghost:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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

.status[data-status="failed"],
.status[data-status="dropped"] {
  background: rgba(248, 113, 113, 0.2);
  color: #f87171;
}

.status[data-status="duplicated"] {
  background: rgba(167, 139, 250, 0.2);
  color: #c4b5fd;
}

.detail header {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.35rem;
}

.detail header span {
  color: rgba(148, 163, 184, 0.9);
}

.detail-meta {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0.85rem;
  font-size: 0.9rem;
  color: #cbd5f5;
}

.detail-meta strong {
  color: #94a3b8;
}

.detail-meta div {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  padding: 0.75rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 10px;
  background: rgba(15, 23, 42, 0.6);
  min-height: 80px;
}

.meta-id {
  margin-left: 0.25rem;
  color: rgba(148, 163, 184, 0.8);
  font-size: 0.78rem;
}

.detail-meta span,
.detail-meta small {
  word-break: break-all;
}

.flag-value {
  background: rgba(15, 23, 42, 0.9);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 8px;
  padding: 0.75rem;
  color: #f8fafc;
  font-family: "JetBrains Mono", Menlo, monospace;
  max-height: 180px;
  overflow: auto;
  word-break: break-all;
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

.attempts-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}

.attempts-table th,
.attempts-table td {
  padding: 0.5rem;
  border-bottom: 1px solid rgba(148, 163, 184, 0.15);
}

.empty {
  text-align: center;
  padding: 1rem;
  color: rgba(148, 163, 184, 0.8);
}

@media (max-width: 960px) {
  .layout {
    grid-template-columns: 1fr;
  }
}
</style>
