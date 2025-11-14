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

    <section class="matrix-section">
      <div class="matrix-header">
        <div>
          <h3>Attack Coverage Matrix</h3>
          <small class="hint">窗口：{{ matrixWindowLabel }}</small>
        </div>
        <div class="matrix-controls">
          <label>
            范围
            <select v-model.number="rangeMinutes">
              <option v-for="option in matrixRangeOptions" :key="option" :value="option">
                {{ option }}m
              </option>
            </select>
          </label>
          <button class="control" @click="shiftMatrix(-1)" :disabled="matrixLoading">←</button>
          <button class="control" @click="shiftMatrix(1)" :disabled="matrixLoading">→</button>
          <button class="control" @click="setMatrixNow" :disabled="matrixLoading">Now</button>
        </div>
      </div>

      <div class="matrix-custom">
        <div class="custom-field">
          <span>开始</span>
          <input type="datetime-local" v-model="customStart" />
        </div>
        <div class="custom-field">
          <span>结束</span>
          <input type="datetime-local" v-model="customEnd" />
        </div>
        <button class="control primary" @click="applyCustomWindow" :disabled="matrixLoading">
          应用窗口
        </button>
      </div>
      <p v-if="matrixInputError" class="error-text matrix-error">{{ matrixInputError }}</p>

      <div class="matrix-body">
        <div v-if="matrixLoading" class="matrix-empty">Loading matrix…</div>
        <div v-else-if="matrixError" class="matrix-empty error">{{ matrixError }}</div>
        <template v-else>
          <div v-if="matrixRows.length && matrixChallenges.length" class="matrix-grid">
            <div class="matrix-row matrix-header-row" :style="matrixColumnStyle">
              <div class="matrix-cell team-header">队伍 / 题目</div>
              <div
                class="matrix-cell challenge-header clickable"
                v-for="challenge in matrixChallenges"
                :key="challenge.id"
                @click="handleChallengeHeaderClick(challenge.id)"
              >
                {{ challenge.name }}
              </div>
            </div>
            <div class="matrix-row" v-for="row in matrixRows" :key="row.team" :style="matrixColumnStyle">
              <div class="matrix-cell team-label clickable" @click="handleTeamClick(row.team)">
                {{ row.team }}
              </div>
              <div
                class="matrix-cell status-cell clickable"
                v-for="cell in row.cells"
                :key="row.team + '-' + cell.challengeId"
                :data-status="cell.status"
                @click="handleMatrixCellClick(row.team, cell.challengeId)"
              >
                <div v-if="tooltipItems(cell).length" class="matrix-tooltip">
                  <strong>{{ tooltipLabel(cell) }}</strong>
                  <ul>
                    <li v-for="name in tooltipItems(cell)" :key="name">{{ name }}</li>
                  </ul>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="matrix-empty">No data for selected window</div>
        </template>
      </div>
    </section>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { fetchOverviewStats, fetchOverviewMatrix } from "@/api/overview";
import type {
  OverviewStats,
  OverviewMatrix,
  OverviewMatrixCell,
  OverviewMatrixStatus
} from "@/types";
import { useExploitStore } from "@/stores/exploits";
import { useTargetStore } from "@/stores/targets";
import { useRunStore, type RunFilters } from "@/stores/runs";
import { useRunnerStore } from "@/stores/runners";

const router = useRouter();
const exploitStore = useExploitStore();
const targetStore = useTargetStore();
const runStore = useRunStore();
const runnerStore = useRunnerStore();

const busy = ref(false);
const overview = ref<OverviewStats>({
  exploitCount: 0,
  targetCount: 0,
  pendingFlags: 0,
  runningRuns: 0,
  flagStatuses: {}
});

const matrix = ref<OverviewMatrix | null>(null);
const matrixLoading = ref(false);
const matrixError = ref<string | null>(null);
const matrixRangeOptions = [5, 15, 30, 60];

const rangeMinutes = ref(15);
const endTime = ref(new Date());
const startTime = ref(new Date(endTime.value.getTime() - rangeMinutes.value * 60 * 1000));
const customStart = ref("");
const customEnd = ref("");
const matrixInputError = ref<string | null>(null);

watch(rangeMinutes, (minutes, previous) => {
  if (previous === undefined) {
    return;
  }
  const delta = minutes * 60 * 1000;
  startTime.value = new Date(endTime.value.getTime() - delta);
  loadMatrix();
});

watch(
  () => [startTime.value.getTime(), endTime.value.getTime()],
  () => {
    customStart.value = formatInputDate(startTime.value);
    customEnd.value = formatInputDate(endTime.value);
  },
  { immediate: true }
);

const exploitCount = computed(() => overview.value.exploitCount);
const targetCount = computed(() => overview.value.targetCount);
const runningRuns = computed(() => overview.value.runningRuns);

const flagStatuses = computed(() =>
  Object.entries(overview.value.flagStatuses ?? {}).map(([label, count]) => ({ label, count }))
);

const matrixChallenges = computed(() => matrix.value?.challenges ?? []);
const matrixColumnStyle = computed(() => ({
  gridTemplateColumns: `repeat(${Math.max(1, (matrixChallenges.value.length || 0) + 1)}, minmax(120px, 1fr))`
}));

const matrixRows = computed(() => {
  if (!matrix.value) {
    return [];
  }
  const index = new Map<string, OverviewMatrixCell>();
  for (const cell of matrix.value.cells ?? []) {
    index.set(`${cell.teamName}|${cell.challengeId}`, cell);
  }
  return (matrix.value.teams ?? []).map((team) => {
    const cells = matrixChallenges.value.map((challenge) => {
      const key = `${team.name}|${challenge.id}`;
      const baseCell =
        index.get(key) ??
        ({
          challengeId: challenge.id,
          teamName: team.name,
          status: "none",
          successExploits: [],
          failedExploits: []
        } as OverviewMatrixCell);
      const status: OverviewMatrixStatus =
        baseCell.successExploits && baseCell.successExploits.length
          ? "success"
          : baseCell.failedExploits && baseCell.failedExploits.length
            ? "failed"
            : "none";
      return {
        ...baseCell,
        status
      };
    });
    return { team: team.name, cells };
  });
});

const matrixWindowLabel = computed(
  () => `${formatTimestamp(startTime.value)} → ${formatTimestamp(endTime.value)}`
);

function formatTimestamp(date: Date): string {
  return date.toLocaleString();
}

function formatInputDate(date: Date): string {
  const pad = (value: number) => value.toString().padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours()
  )}:${pad(date.getMinutes())}`;
}

function parseInputDate(value: string): Date | null {
  if (!value) {
    return null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function tooltipItems(cell: OverviewMatrixCell): string[] {
  if (cell.successExploits?.length) {
    return cell.successExploits;
  }
  if (cell.failedExploits?.length) {
    return cell.failedExploits;
  }
  return [];
}

function tooltipLabel(cell: OverviewMatrixCell): string {
  if (cell.successExploits?.length) {
    return "成功 EXP";
  }
  if (cell.failedExploits?.length) {
    return "失败 EXP";
  }
  return "";
}

function goToRunsWithFilters(filters: RunFilters) {
  runStore.primeFilters(filters);
  router.push({ name: "runs" });
}

function handleChallengeHeaderClick(challengeId: string) {
  if (!challengeId) {
    return;
  }
  goToRunsWithFilters({ challengeId });
}

function handleTeamClick(teamName: string) {
  if (!teamName) {
    return;
  }
  goToRunsWithFilters({ targetName: teamName });
}

function handleMatrixCellClick(teamName: string, challengeId: string) {
  if (!teamName && !challengeId) {
    return;
  }
  goToRunsWithFilters({
    targetName: teamName || undefined,
    challengeId: challengeId || undefined
  });
}

async function loadOverviewStats() {
  overview.value = await fetchOverviewStats();
}

async function loadMatrix() {
  matrixLoading.value = true;
  matrixError.value = null;
  try {
    matrix.value = await fetchOverviewMatrix({
      start: startTime.value.toISOString(),
      end: endTime.value.toISOString()
    });
  } catch (err) {
    console.error(err);
    matrixError.value = "Failed to load matrix";
  } finally {
    matrixLoading.value = false;
  }
}

function shiftMatrix(direction: number) {
  const delta = rangeMinutes.value * 60 * 1000 * direction;
  startTime.value = new Date(startTime.value.getTime() + delta);
  endTime.value = new Date(endTime.value.getTime() + delta);
  loadMatrix();
}

function setMatrixNow() {
  endTime.value = new Date();
  startTime.value = new Date(endTime.value.getTime() - rangeMinutes.value * 60 * 1000);
  loadMatrix();
}

function applyCustomWindow() {
  matrixInputError.value = null;
  const parsedStart = parseInputDate(customStart.value);
  const parsedEnd = parseInputDate(customEnd.value);
  if (!parsedStart || !parsedEnd) {
    matrixInputError.value = "请输入有效的开始和结束时间";
    return;
  }
  if (parsedEnd.getTime() <= parsedStart.getTime()) {
    matrixInputError.value = "结束时间必须晚于开始时间";
    return;
  }
  startTime.value = parsedStart;
  endTime.value = parsedEnd;
  const diffMinutes = Math.max(
    1,
    Math.round((parsedEnd.getTime() - parsedStart.getTime()) / (60 * 1000))
  );
  if (rangeMinutes.value !== diffMinutes) {
    rangeMinutes.value = diffMinutes;
  } else {
    loadMatrix();
  }
}

async function refreshAll() {
  busy.value = true;
  await Promise.all([
    exploitStore.load(),
    targetStore.load(),
    runStore.refresh(),
    runnerStore.load(),
    loadOverviewStats(),
    loadMatrix()
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

.matrix-section {
  background: rgba(30, 41, 59, 0.7);
  border-radius: 12px;
  padding: 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
  display: flex;
  flex-direction: column;
  gap: 1rem;
  overflow: hidden;
}

.matrix-header {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 1rem;
  align-items: center;
}

.matrix-controls {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.matrix-controls label {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  font-size: 0.85rem;
}

.matrix-controls select {
  background: rgba(15, 23, 42, 0.9);
  color: #e2e8f0;
  border: 1px solid rgba(148, 163, 184, 0.4);
  border-radius: 8px;
  padding: 0.25rem 0.5rem;
}

.matrix-controls .control {
  background: rgba(59, 130, 246, 0.2);
  color: #bae6fd;
  border: 1px solid rgba(59, 130, 246, 0.4);
  border-radius: 8px;
  padding: 0.3rem 0.75rem;
  cursor: pointer;
}

.matrix-controls .control:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.matrix-body {
  width: 100%;
  overflow-x: auto;
}

.matrix-custom {
  display: flex;
  align-items: flex-end;
  gap: 1rem;
  flex-wrap: wrap;
  margin-bottom: 0.25rem;
}

.matrix-custom .custom-field {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  font-size: 0.82rem;
  color: #94a3b8;
}

.matrix-custom input {
  background: rgba(15, 23, 42, 0.85);
  border: 1px solid rgba(148, 163, 184, 0.5);
  border-radius: 10px;
  padding: 0.4rem 0.6rem;
  color: #e2e8f0;
  min-width: 190px;
}

.matrix-custom .control.primary {
  border-radius: 999px;
  padding: 0.4rem 1rem;
  background: #38bdf8;
  color: #0f172a;
  border: none;
  font-weight: 600;
}

.matrix-error {
  margin: 0;
}

.matrix-grid {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.matrix-row {
  display: grid;
  gap: 0.25rem;
}

.matrix-cell {
  border-radius: 6px;
  padding: 0.35rem 0.5rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
  min-height: 38px;
  display: flex;
  align-items: center;
  justify-content: center;
  text-align: center;
  font-size: 0.85rem;
  background: rgba(15, 23, 42, 0.4);
}

.matrix-cell.clickable {
  cursor: pointer;
  transition: background 0.15s ease, opacity 0.15s ease;
}

.matrix-cell.clickable:active {
  opacity: 0.85;
}

.team-header,
.team-label {
  font-weight: 600;
  text-align: left;
  justify-content: flex-start;
  background: rgba(37, 99, 235, 0.15);
}

.challenge-header {
  font-weight: 600;
}

.status-cell {
  cursor: default;
  transition: transform 0.1s ease;
  position: relative;
}

.status-cell:hover {
  transform: scale(1.02);
}

.status-cell[data-status="success"] {
  background: rgba(34, 197, 94, 0.85);
  color: #052e16;
}

.status-cell[data-status="failed"] {
  background: rgba(248, 113, 113, 0.9);
  color: #450a0a;
}

.status-cell[data-status="none"] {
  background: rgba(51, 65, 85, 0.6);
  color: #cbd5f5;
}

.matrix-empty {
  text-align: center;
  padding: 1rem;
  border: 1px dashed rgba(148, 163, 184, 0.4);
  border-radius: 8px;
  color: #cbd5f5;
}

.matrix-tooltip {
  display: none;
  position: absolute;
  top: 0;
  left: 0;
  transform: translateY(-100%);
  background: rgba(15, 23, 42, 0.95);
  border: 1px solid rgba(148, 163, 184, 0.5);
  border-radius: 8px;
  padding: 0.5rem 0.75rem;
  min-width: 160px;
  text-align: left;
  z-index: 5;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.35);
}

.status-cell:hover .matrix-tooltip {
  display: block;
}

.matrix-tooltip strong {
  display: block;
  font-size: 0.75rem;
  margin-bottom: 0.25rem;
  color: #e0f2fe;
}

.matrix-tooltip ul {
  list-style: disc;
  padding-left: 1rem;
  margin: 0;
  color: #cbd5f5;
  font-size: 0.75rem;
}

.matrix-empty.error {
  color: #fca5a5;
  border-color: rgba(248, 113, 113, 0.6);
}

article li small {
  color: #94a3b8;
}
</style>
