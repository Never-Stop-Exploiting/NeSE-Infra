<template>
  <section class="grid">
    <article class="panel">
      <header>
        <h2>Add Target</h2>
        <p>Register a single target service for a team.</p>
      </header>
      <form class="form" @submit.prevent="handleCreateTarget">
        <label>
          Challenge
          <select
            v-model="singleForm.challengeId"
            required
            :disabled="!challengeOptions.length"
          >
            <option value="" disabled>Select challenge</option>
            <option v-for="challenge in challengeOptions" :key="challenge.id" :value="challenge.id">
              {{ challenge.name || challenge.id }}
            </option>
          </select>
        </label>
        <label>
          Team Name
          <input v-model="singleForm.teamName" required placeholder="Team 01" />
        </label>
        <label class="row">
          <span>IP</span>
          <input v-model="singleForm.ip" required placeholder="10.0.0.5" />
          <span>Port</span>
          <input v-model.number="singleForm.port" type="number" required min="1" max="65535" />
        </label>
        <label>
          Concurrency Limit
          <input v-model.number="singleForm.concurrencyLimit" type="number" min="1" max="32" required />
        </label>
        <button type="submit" :disabled="pending">
          {{ pending ? "Saving..." : "Add Target" }}
        </button>
      </form>
    </article>

    <article class="panel">
      <header>
        <h2>Bulk Import</h2>
        <p>Generate sequential targets using an IP base or team list.</p>
      </header>
      <form class="form" @submit.prevent="handleImport">
        <label>
          Challenge
          <select
            v-model="bulkForm.challengeId"
            required
            :disabled="!challengeOptions.length"
          >
            <option value="" disabled>Select challenge</option>
            <option v-for="challenge in challengeOptions" :key="challenge.id" :value="challenge.id">
              {{ challenge.name || challenge.id }}
            </option>
          </select>
        </label>
        <label class="row">
          <span>Base IP</span>
          <input v-model="bulkForm.baseIp" required placeholder="10.0.0.10" />
          <span>Count</span>
          <input v-model.number="bulkForm.count" type="number" min="1" max="128" required />
        </label>
        <label>
          Port
          <input v-model.number="bulkForm.port" type="number" min="1" max="65535" required />
        </label>
        <label>
          Team Names (optional, one per line)
          <textarea v-model="bulkForm.teamNames" rows="4" placeholder="Team A\nTeam B"></textarea>
        </label>
        <div class="preview">
          <strong>Preview</strong>
          <ul>
            <li v-for="item in previewTargets" :key="item">{{ item }}</li>
          </ul>
        </div>
        <button type="submit" :disabled="pending">
          {{ pending ? "Importing..." : `Import ${bulkForm.count} Targets` }}
        </button>
      </form>
    </article>

    <article class="panel table">
      <header>
        <h2>Registered Targets</h2>
        <p>{{ targetStore.targets.length }} items</p>
      </header>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>Challenge</th>
              <th>Team</th>
              <th>Address</th>
              <th>Status</th>
              <th>Concurrency</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="target in targetStore.targets" :key="target.id">
              <td>{{ challengeNames[target.challengeId] ?? target.challengeId }}</td>
              <td>{{ target.teamName }}</td>
              <td>{{ target.ip }}:{{ target.port }}</td>
              <td>{{ target.status ?? "active" }}</td>
              <td>{{ target.concurrencyLimit }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </article>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted, watch } from "vue";
import { storeToRefs } from "pinia";
import { useTargetStore } from "@/stores/targets";
import { useChallengeStore } from "@/stores/challenges";

const targetStore = useTargetStore();
const challengeStore = useChallengeStore();
const { challenges } = storeToRefs(challengeStore);
const pending = ref(false);
const challengeOptions = computed(() => challenges.value);
const challengeNames = computed<Record<string, string>>(() => {
  const mapping: Record<string, string> = {};
  challenges.value.forEach((challenge) => {
    mapping[challenge.id] = challenge.name || challenge.id;
  });
  return mapping;
});

const singleForm = reactive({
  challengeId: "",
  teamName: "",
  ip: "",
  port: 80,
  concurrencyLimit: 1
});

const bulkForm = reactive({
  challengeId: "",
  baseIp: "",
  port: 80,
  count: 5,
  teamNames: ""
});

const previewTargets = computed(() => {
  if (!bulkForm.baseIp || bulkForm.count <= 0) return [];
  const previews: string[] = [];
  const base = bulkForm.baseIp.split(".").map(Number);
  if (base.length !== 4 || base.some((segment) => Number.isNaN(segment))) {
    return ["Invalid IP"];
  }
  for (let i = 0; i < Math.min(5, bulkForm.count); i++) {
    const ip = [...base];
    ip[3] = (ip[3] + i) % 256;
    const team = bulkForm.teamNames.split(/\r?\n/)[i] ?? `Team ${i + 1}`;
    previews.push(`${team} → ${ip.join(".")}:${bulkForm.port}`);
  }
  if (bulkForm.count > 5) {
    previews.push(`… and ${bulkForm.count - 5} more`);
  }
  return previews;
});

watch(
  challenges,
  (items) => {
    const firstId = items[0]?.id ?? "";
    if (!items.length) {
      singleForm.challengeId = "";
      bulkForm.challengeId = "";
      return;
    }
    if (!items.some((item) => item.id === singleForm.challengeId)) {
      singleForm.challengeId = firstId;
    }
    if (!items.some((item) => item.id === bulkForm.challengeId)) {
      bulkForm.challengeId = firstId;
    }
  },
  { immediate: true }
);

async function handleCreateTarget() {
  pending.value = true;
  await targetStore.addTarget({
    challengeId: singleForm.challengeId,
    teamName: singleForm.teamName,
    ip: singleForm.ip,
    port: singleForm.port,
    concurrencyLimit: singleForm.concurrencyLimit,
    status: "active"
  });
  pending.value = false;
  singleForm.teamName = "";
  singleForm.ip = "";
}

async function handleImport() {
  pending.value = true;
  const teamNames = bulkForm.teamNames
    .split(/\r?\n/)
    .map((name) => name.trim())
    .filter(Boolean);
  await targetStore.bulkImport({
    challengeId: bulkForm.challengeId,
    baseIp: bulkForm.baseIp,
    port: bulkForm.port,
    count: bulkForm.count,
    teamNames: teamNames.length ? teamNames : undefined
  });
  pending.value = false;
}

onMounted(() => {
  challengeStore.load();
  targetStore.load();
});
</script>

<style scoped>
.grid {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
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

.table {
  grid-column: 1 / -1;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

label {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  font-size: 0.9rem;
}

input,
textarea {
  padding: 0.6rem 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  background: rgba(15, 23, 42, 0.8);
  color: #f8fafc;
}

.row {
  display: grid;
  grid-template-columns: auto 1fr auto 1fr;
  align-items: center;
  gap: 0.5rem;
}

button {
  align-self: flex-start;
  background: #38bdf8;
  color: #0f172a;
  border: none;
  padding: 0.6rem 1.6rem;
  border-radius: 999px;
  font-weight: 600;
  cursor: pointer;
}

.preview {
  background: rgba(15, 23, 42, 0.8);
  border-radius: 8px;
  padding: 0.75rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
}

.preview ul {
  list-style: none;
  padding: 0;
  margin: 0.5rem 0 0;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
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
  padding: 0.6rem 0.75rem;
  border-bottom: 1px solid rgba(148, 163, 184, 0.15);
}

th {
  text-align: left;
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #94a3b8;
}
</style>
