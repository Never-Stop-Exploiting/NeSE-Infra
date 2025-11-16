<template>
  <section class="grid">
    <article class="panel card">
      <header>
        <h2>Create Challenge</h2>
        <p>Define metadata before attaching targets or exploits.</p>
      </header>
      <form class="form" @submit.prevent="handleCreate">
        <div class="form-grid">
          <div class="form-field span-2">
            <label>Challenge Name</label>
            <input v-model="createForm.name" required placeholder="RCE 101" />
            <small class="hint">Pick something short but descriptive.</small>
          </div>
          <div class="form-field">
            <label>Category</label>
            <input v-model="createForm.category" placeholder="pwn/web/rev" />
          </div>
          <div class="form-field">
            <label>Service Type</label>
            <input v-model="createForm.serviceType" placeholder="tcp/http" />
          </div>
          <div class="form-field span-2">
            <label>Flag Regex</label>
            <input v-model="createForm.flagRegex" required />
            <small class="hint">
              Runners will match stdout/flag files with this expression.
            </small>
          </div>
          <div class="form-field span-2">
            <label>Description</label>
            <textarea
              v-model="createForm.description"
              rows="3"
              placeholder="Short summary or reminders for the team."
            ></textarea>
          </div>
        </div>
        <div class="form-actions">
          <button type="submit" :disabled="pendingCreate">
            {{ pendingCreate ? "Creating..." : "Create Challenge" }}
          </button>
        </div>
      </form>
    </article>

    <article class="panel table card">
      <header>
        <h2>Challenges</h2>
        <p>{{ challenges.length }} total</p>
      </header>
      <div class="table-wrapper">
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Category</th>
              <th>Service</th>
              <th>Description</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!challenges.length">
              <td colspan="5" class="empty">No challenges yet. Create one above.</td>
            </tr>
            <tr v-for="challenge in challenges" :key="challenge.id">
              <td>
                <div class="title">
                  <strong>{{ challenge.name }}</strong>
                  <small>{{ challenge.id }}</small>
                </div>
              </td>
              <td>{{ challenge.category || "—" }}</td>
              <td>{{ challenge.serviceType || "—" }}</td>
              <td>{{ challenge.description || "—" }}</td>
              <td class="actions">
                <button type="button" @click="startEdit(challenge)">Edit</button>
                <button
                  type="button"
                  class="danger"
                  :disabled="deletePending === challenge.id"
                  @click="handleDelete(challenge.id)"
                >
                  {{ deletePending === challenge.id ? "Deleting..." : "Delete" }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </article>

    <article v-if="editing" class="panel card">
      <header>
        <h2>Edit Challenge</h2>
        <p>Update metadata for {{ editForm.name || editForm.id }}</p>
      </header>
      <form class="form" @submit.prevent="handleUpdate">
        <div class="form-grid">
          <div class="form-field span-2">
            <label>Challenge Name</label>
            <input v-model="editForm.name" required />
          </div>
          <div class="form-field">
            <label>Category</label>
            <input v-model="editForm.category" />
          </div>
          <div class="form-field">
            <label>Service Type</label>
            <input v-model="editForm.serviceType" />
          </div>
          <div class="form-field span-2">
            <label>Flag Regex</label>
            <input v-model="editForm.flagRegex" required />
            <small class="hint">
              Overrides the default runner regex for this challenge.
            </small>
          </div>
          <div class="form-field span-2">
            <label>Description</label>
            <textarea v-model="editForm.description" rows="3"></textarea>
          </div>
        </div>
        <div class="form-actions row-buttons">
          <button type="submit" :disabled="pendingEdit">
            {{ pendingEdit ? "Saving..." : "Save Changes" }}
          </button>
          <button type="button" class="ghost" @click="cancelEdit" :disabled="pendingEdit">Cancel</button>
        </div>
      </form>
    </article>
  </section>
</template>

<script setup lang="ts">
import { reactive, ref, computed, onMounted } from "vue";
import { storeToRefs } from "pinia";
import { useChallengeStore } from "@/stores/challenges";
import type { Challenge } from "@/types";

const challengeStore = useChallengeStore();
const { challenges } = storeToRefs(challengeStore);

const DEFAULT_FLAG_REGEX = "flag\\{[a-zA-Z0-9_\\-]+\\}";

const pendingCreate = ref(false);
const pendingEdit = ref(false);
const deletePending = ref<string | null>(null);

const createForm = reactive({
  name: "",
  category: "",
  serviceType: "",
  description: "",
  flagRegex: DEFAULT_FLAG_REGEX
});

const editForm = reactive({
  id: "",
  name: "",
  category: "",
  serviceType: "",
  description: "",
  flagRegex: DEFAULT_FLAG_REGEX
});

const editing = computed(() => !!editForm.id);

function resetCreateForm() {
  createForm.name = "";
  createForm.category = "";
  createForm.serviceType = "";
  createForm.description = "";
  createForm.flagRegex = DEFAULT_FLAG_REGEX;
}

function setEditForm(challenge: Challenge) {
  editForm.id = challenge.id;
  editForm.name = challenge.name;
  editForm.category = challenge.category ?? "";
  editForm.serviceType = challenge.serviceType ?? "";
  editForm.description = challenge.description ?? "";
  editForm.flagRegex = challenge.flagRegex ?? DEFAULT_FLAG_REGEX;
}

function clearEditForm() {
  editForm.id = "";
  editForm.name = "";
  editForm.category = "";
  editForm.serviceType = "";
  editForm.description = "";
  editForm.flagRegex = DEFAULT_FLAG_REGEX;
}

async function handleCreate() {
  pendingCreate.value = true;
  try {
    await challengeStore.create({
      name: createForm.name,
      category: createForm.category || undefined,
      serviceType: createForm.serviceType || undefined,
      description: createForm.description || undefined,
      flagRegex: createForm.flagRegex || undefined
    });
    resetCreateForm();
  } finally {
    pendingCreate.value = false;
  }
}

function startEdit(challenge: Challenge) {
  setEditForm(challenge);
}

function cancelEdit() {
  if (pendingEdit.value) return;
  clearEditForm();
}

async function handleUpdate() {
  if (!editForm.id) return;
  pendingEdit.value = true;
  try {
    await challengeStore.update(editForm.id, {
      name: editForm.name,
      category: editForm.category || undefined,
      serviceType: editForm.serviceType || undefined,
      description: editForm.description || undefined,
      flagRegex: editForm.flagRegex || undefined
    });
    clearEditForm();
  } finally {
    pendingEdit.value = false;
  }
}

async function handleDelete(id: string) {
  if (!confirm("Delete this challenge? Related targets and exploits will need manual cleanup.")) {
    return;
  }
  deletePending.value = id;
  try {
    await challengeStore.remove(id);
    if (editForm.id === id) {
      clearEditForm();
    }
  } finally {
    deletePending.value = null;
  }
}

onMounted(() => {
  challengeStore.load();
});
</script>

<style scoped>
.grid {
  display: grid;
  gap: 1.5rem;
  grid-template-columns: minmax(320px, 420px) minmax(0, 1fr);
  align-items: start;
}

.panel {
  border-radius: 18px;
  padding: 1.5rem;
  border: 1px solid rgba(148, 163, 184, 0.2);
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.card {
  background: linear-gradient(135deg, rgba(30, 41, 59, 0.85), rgba(15, 23, 42, 0.9));
  box-shadow: 0 20px 55px rgba(15, 23, 42, 0.55);
}

.table {
  grid-column: 2;
  grid-row: 1 / span 2;
}

@media (max-width: 1024px) {
  .grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .table {
    grid-column: 1;
    grid-row: auto;
  }
}

.form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.form-grid {
  display: grid;
  gap: 0.9rem 1rem;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  width: 100%;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.form-field label {
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #94a3b8;
}

.form-field input,
.form-field textarea {
  width: 100%;
  background: rgba(15, 23, 42, 0.85);
  border: 1px solid rgba(148, 163, 184, 0.35);
  border-radius: 10px;
  padding: 0.55rem 0.75rem;
  color: #e2e8f0;
  font-size: 0.95rem;
}

.form-field textarea {
  resize: vertical;
}

.form-grid .span-2 {
  grid-column: span 2;
}

@media (max-width: 640px) {
  .form-grid .span-2 {
    grid-column: 1 / -1;
  }
}

.form button {
  justify-self: flex-start;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
}

.hint {
  display: block;
  font-size: 0.75rem;
  color: #94a3b8;
  margin-top: 0.2rem;
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
  text-align: left;
  border-bottom: 1px solid rgba(148, 163, 184, 0.12);
}

.empty {
  text-align: center;
  padding: 2rem;
  color: #94a3b8;
}

.title {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.title small {
  color: #94a3b8;
  font-family: "JetBrains Mono", monospace;
  font-size: 0.75rem;
}

.actions {
  display: flex;
  gap: 0.5rem;
}

button {
  border: none;
  border-radius: 999px;
  background: linear-gradient(100deg, #38bdf8, #60a5fa);
  color: #0f172a;
  font-weight: 600;
  padding: 0.55rem 1.4rem;
  cursor: pointer;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

button:disabled {
  opacity: 0.6;
  cursor: wait;
}

button:not(:disabled):hover {
  transform: translateY(-1px);
  box-shadow: 0 8px 25px rgba(56, 189, 248, 0.35);
}

.danger {
  background: linear-gradient(100deg, #fb7185, #f97316);
  color: #0f172a;
}

.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.4);
  color: #e2e8f0;
  border-radius: 10px;
}

.row-buttons {
  display: flex;
  gap: 0.75rem;
  grid-column: 1 / -1;
  justify-content: flex-end;
}
</style>
