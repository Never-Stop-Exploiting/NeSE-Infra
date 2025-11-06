<template>
  <section class="grid">
    <article class="panel">
      <header>
        <h2>Create Challenge</h2>
        <p>Define metadata before attaching targets or exploits.</p>
      </header>
      <form class="form" @submit.prevent="handleCreate">
        <label>
          Name
          <input v-model="createForm.name" required placeholder="RCE 101" />
        </label>
        <label>
          Category
          <input v-model="createForm.category" placeholder="pwn/web/rev" />
        </label>
        <label>
          Service Type
          <input v-model="createForm.serviceType" placeholder="tcp/http" />
        </label>
        <label class="span">
          Description
          <textarea v-model="createForm.description" rows="3" placeholder="Short summary"></textarea>
        </label>
        <button type="submit" :disabled="pendingCreate">
          {{ pendingCreate ? "Creating..." : "Create Challenge" }}
        </button>
      </form>
    </article>

    <article class="panel table">
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

    <article v-if="editing" class="panel">
      <header>
        <h2>Edit Challenge</h2>
        <p>Update metadata for {{ editForm.name || editForm.id }}</p>
      </header>
      <form class="form" @submit.prevent="handleUpdate">
        <label>
          Name
          <input v-model="editForm.name" required />
        </label>
        <label>
          Category
          <input v-model="editForm.category" />
        </label>
        <label>
          Service Type
          <input v-model="editForm.serviceType" />
        </label>
        <label class="span">
          Description
          <textarea v-model="editForm.description" rows="3"></textarea>
        </label>
        <div class="row-buttons">
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

const pendingCreate = ref(false);
const pendingEdit = ref(false);
const deletePending = ref<string | null>(null);

const createForm = reactive({
  name: "",
  category: "",
  serviceType: "",
  description: ""
});

const editForm = reactive({
  id: "",
  name: "",
  category: "",
  serviceType: "",
  description: ""
});

const editing = computed(() => !!editForm.id);

function resetCreateForm() {
  createForm.name = "";
  createForm.category = "";
  createForm.serviceType = "";
  createForm.description = "";
}

function setEditForm(challenge: Challenge) {
  editForm.id = challenge.id;
  editForm.name = challenge.name;
  editForm.category = challenge.category ?? "";
  editForm.serviceType = challenge.serviceType ?? "";
  editForm.description = challenge.description ?? "";
}

function clearEditForm() {
  editForm.id = "";
  editForm.name = "";
  editForm.category = "";
  editForm.serviceType = "";
  editForm.description = "";
}

async function handleCreate() {
  pendingCreate.value = true;
  try {
    await challengeStore.create({
      name: createForm.name,
      category: createForm.category || undefined,
      serviceType: createForm.serviceType || undefined,
      description: createForm.description || undefined
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
      description: editForm.description || undefined
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
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
}

.form .span {
  grid-column: 1 / -1;
}

.form button {
  justify-self: flex-start;
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
  border-radius: 8px;
  background: #38bdf8;
  color: #0f172a;
  font-weight: 600;
  padding: 0.5rem 1rem;
  cursor: pointer;
}

button:disabled {
  opacity: 0.6;
  cursor: wait;
}

.danger {
  background: #f87171;
  color: #0f172a;
}

.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.4);
  color: #e2e8f0;
}

.row-buttons {
  display: flex;
  gap: 0.75rem;
  grid-column: 1 / -1;
}
</style>
