<template>
  <section class="layout">
    <article class="panel table">
      <header>
        <h2>Flags</h2>
        <div class="actions">
          <label class="filter">
            <span>Status</span>
            <select v-model="statusFilter" @change="changeFilter">
              <option v-for="option in statusOptions" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          </label>
          <button class="refresh" :disabled="flagStore.loading" @click="flagStore.refresh">
            {{ flagStore.loading ? "Refreshing..." : "Refresh" }}
          </button>
        </div>
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
              <td>
                <code :title="flag.value">{{ truncateFlag(flag.value) }}</code>
              </td>
              <td><span class="status" :data-status="flag.status">{{ flag.status }}</span></td>
              <td>{{ flag.submissionCount }}</td>
              <td>{{ flag.challengeName || flag.challengeId || "—" }}</td>
              <td>{{ flag.targetLabel || flag.targetId || "—" }}</td>
              <td>
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
              <td colspan="6" class="empty">No flags found</td>
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
        <span>Status: {{ selectedFlag.status }}</span>
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
import { useFlagStore } from "@/stores/flags";
import type { FlagRecord } from "@/types";
import { fetchFlagDetail } from "@/api/flags";

const flagStore = useFlagStore();
const selectedFlagId = ref<string | null>(null);
const selectedFlagDetail = ref<FlagRecord | null>(null);
const detailLoading = ref(false);
const detailError = ref<string | null>(null);
const logOutput = ref("Select a flag to view logs");
const statusFilter = ref("");
const retryingFlag = ref<string | null>(null);

const selectedFlag = computed(() =>
  flagStore.flags.find((flag) => flag.id === selectedFlagId.value)
);

const statusOptions = [
  { label: "All", value: "" },
  { label: "New", value: "new" },
  { label: "Submitting", value: "submitting" },
  { label: "Accepted", value: "accepted" },
  { label: "Rejected", value: "rejected" },
  { label: "Duplicate", value: "duplicate" },
  { label: "Retry", value: "retry" },
  { label: "Dropped", value: "dropped" }
];

watch(
  () => flagStore.statusFilter,
  (value) => {
    statusFilter.value = value;
  },
  { immediate: true }
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

async function changeFilter() {
  await flagStore.setStatusFilter(statusFilter.value);
  selectedFlagId.value = null;
  selectedFlagDetail.value = null;
  logOutput.value = "Select a flag to view logs";
}

function formatTimestamp(value?: string) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
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

.actions {
  display: flex;
  gap: 0.75rem;
  align-items: center;
}

.filter {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  font-size: 0.85rem;
  color: #94a3b8;
}

.filter select {
  background: rgba(15, 23, 42, 0.85);
  color: #f8fafc;
  border: 1px solid rgba(148, 163, 184, 0.3);
  border-radius: 999px;
  padding: 0.35rem 0.9rem;
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

.status[data-status="duplicate"] {
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
