<template>
  <section class="settings">
    <header class="settings__header">
      <div>
        <p class="eyebrow">Control Panel</p>
        <h2>Submitter Package</h2>
        <p class="subtitle">Manage submit.sh and its attachments. Sync to push updates to all submitter instances.</p>
      </div>
      <div class="header-actions">
        <span class="version">Version: {{ version || "unknown" }}</span>
        <button type="button" class="ghost" :disabled="syncing" @click="handleSync">
          {{ syncing ? "Syncing..." : "Sync Submitters" }}
        </button>
      </div>
    </header>

    <div class="grid">
      <section class="card">
        <h3>Files</h3>
        <p class="subtitle">Upload any helper files required by submit.sh. Default script lives at /tmp/submitter/submit.sh inside chroot.</p>

        <div
          class="upload"
          :class="{ 'upload--dragging': isDragging }"
          @dragover.prevent="handleDragOver"
          @dragleave.prevent="handleDragLeave"
          @drop.prevent="handleDrop"
        >
          <input ref="fileInput" class="file-input" type="file" @change="handleFileChange" />
          <button type="button" @click="triggerUpload">Choose File</button>
          <span>{{ isDragging ? "Drop file to upload" : selectedFileName }}</span>
        </div>
        <p v-if="fileFeedback" class="feedback" :class="fileFeedbackType">{{ fileFeedback }}</p>

        <div v-if="loadingFiles" class="empty">Loading files...</div>
        <div v-else-if="!files.length" class="empty">No files uploaded yet.</div>
        <table v-else class="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Size</th>
              <th>MD5</th>
              <th>Updated</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="file in files" :key="file.name">
              <td>{{ file.name }}</td>
              <td>{{ formatSize(file.size) }}</td>
              <td>{{ file.md5 }}</td>
              <td>{{ formatTime(file.modTime) }}</td>
              <td class="row-actions">
                <button type="button" @click="handleDownload(file)">Download</button>
                <button type="button" class="danger" @click="handleDelete(file)">Delete</button>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <section class="card">
        <h3>submit.sh</h3>
        <p class="subtitle">Default entry invokes <code>python submit.py "$1"</code>. Edit below to customize.</p>
        <textarea v-model="script" class="script-editor" spellcheck="false"></textarea>
        <div class="actions">
          <button type="button" :disabled="savingScript" @click="saveScript">
            {{ savingScript ? "Saving..." : "Save Script" }}
          </button>
        </div>
        <p v-if="scriptFeedback" class="feedback" :class="scriptFeedbackType">{{ scriptFeedback }}</p>
      </section>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import {
  listSubmitterFiles,
  uploadSubmitterFile,
  deleteSubmitterFile,
  downloadSubmitterFile,
  fetchSubmitterScript,
  saveSubmitterScript,
  syncSubmitterPackage,
  fetchSubmitterVersion
} from "@/api/submitterPackage";
import type { SubmitterFile } from "@/api/submitterPackage";

const files = ref<SubmitterFile[]>([]);
const script = ref("");
const version = ref("");
const loadingFiles = ref(false);
const syncing = ref(false);
const savingScript = ref(false);
const fileInput = ref<HTMLInputElement | null>(null);
const selectedFileName = ref("No file selected");
const fileFeedback = ref<string | null>(null);
const fileFeedbackType = ref<"success" | "error">("success");
const isDragging = ref(false);
let dragCounter = 0;
const scriptFeedback = ref<string | null>(null);
const scriptFeedbackType = ref<"success" | "error">("success");

async function loadFiles() {
  loadingFiles.value = true;
  try {
    files.value = await listSubmitterFiles();
  } catch (error) {
    console.error("Failed to load files", error);
  } finally {
    loadingFiles.value = false;
  }
}

async function loadScript() {
  try {
    script.value = await fetchSubmitterScript();
  } catch (error) {
    console.error("Failed to load script", error);
  }
}

async function loadVersion() {
  try {
    version.value = await fetchSubmitterVersion();
  } catch (error) {
    console.error("Failed to load version", error);
  }
}

function triggerUpload() {
  fileInput.value?.click();
}

async function handleFileChange(event: Event) {
  const target = event.target as HTMLInputElement;
  const file = target.files?.[0];
  if (!file) {
    selectedFileName.value = "No file selected";
    return;
  }
  await performUpload(file);
  if (fileInput.value) {
    fileInput.value.value = "";
  }
  selectedFileName.value = "No file selected";
}

async function performUpload(file: File) {
  selectedFileName.value = file.name;
  fileFeedback.value = null;
  try {
    await uploadSubmitterFile(file);
    await loadFiles();
    fileFeedbackType.value = "success";
    fileFeedback.value = "File uploaded successfully.";
  } catch (error) {
    console.error("Upload failed", error);
    fileFeedbackType.value = "error";
    fileFeedback.value = "Upload failed. Check console for details.";
  } finally {
    selectedFileName.value = "No file selected";
  }
}

async function handleDelete(file: SubmitterFile) {
  if (file.name === "submit.sh" && !confirm("submit.sh cannot be deleted. Overwrite instead.")) {
    return;
  }
  if (!confirm(`Delete ${file.name}?`)) {
    return;
  }
  try {
    await deleteSubmitterFile(file.name);
    await loadFiles();
  } catch (error) {
    console.error("Delete failed", error);
  }
}

async function handleDownload(file: SubmitterFile) {
  try {
    const blob = await downloadSubmitterFile(file.name);
    const url = window.URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = file.name;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    window.URL.revokeObjectURL(url);
  } catch (error) {
    console.error("Download failed", error);
  }
}

function handleDragOver(event: DragEvent) {
  event.preventDefault();
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = "copy";
  }
  dragCounter += 1;
  isDragging.value = true;
}

function handleDragLeave() {
  dragCounter = Math.max(0, dragCounter - 1);
  if (dragCounter === 0) {
    isDragging.value = false;
  }
}

async function handleDrop(event: DragEvent) {
  event.preventDefault();
  dragCounter = 0;
  isDragging.value = false;
  const file = event.dataTransfer?.files?.[0];
  if (!file) {
    return;
  }
  await performUpload(file);
}

async function saveScript() {
  savingScript.value = true;
  scriptFeedback.value = null;
  try {
    await saveSubmitterScript(script.value);
    scriptFeedbackType.value = "success";
    scriptFeedback.value = "Script saved.";
  } catch (error) {
    console.error("Save script failed", error);
    scriptFeedbackType.value = "error";
    scriptFeedback.value = "Failed to save script.";
  } finally {
    savingScript.value = false;
  }
}

async function handleSync() {
  syncing.value = true;
  try {
    version.value = await syncSubmitterPackage();
  } catch (error) {
    console.error("Sync failed", error);
  } finally {
    syncing.value = false;
  }
}

function formatSize(bytes: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let idx = 0;
  let value = bytes;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx += 1;
  }
  return `${value.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`;
}

function formatTime(value?: string): string {
  return value ? new Date(value).toLocaleString() : "-";
}

onMounted(async () => {
  await Promise.all([loadFiles(), loadScript(), loadVersion()]);
});
</script>

<style scoped>
.settings {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.settings__header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
}

.eyebrow {
  text-transform: uppercase;
  letter-spacing: 0.15em;
  font-size: 0.75rem;
  color: #94a3b8;
  margin-bottom: 0.2rem;
}

.subtitle {
  color: #94a3b8;
  margin: 0.25rem 0 0;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 1.5rem;
}

.card {
  background: rgba(15, 23, 42, 0.6);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 12px;
  padding: 1.5rem;
  box-shadow: 0 20px 45px rgba(2, 6, 23, 0.5);
}

.upload {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
  padding: 0.9rem;
  border: 1px dashed rgba(148, 163, 184, 0.4);
  border-radius: 10px;
  transition: border-color 0.2s ease, background 0.2s ease;
}

.upload--dragging {
  border-color: #38bdf8;
  background: rgba(56, 189, 248, 0.08);
}

.file-input {
  display: none;
}

.table {
  width: 100%;
  border-collapse: collapse;
  margin-top: 1rem;
}

.table th,
.table td {
  text-align: left;
  padding: 0.65rem;
  border-bottom: 1px solid rgba(148, 163, 184, 0.1);
}

.table th {
  color: #cbd5f5;
  font-weight: 500;
  font-size: 0.85rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.row-actions {
  display: flex;
  gap: 0.5rem;
}

.script-editor {
  width: 100%;
  min-height: 280px;
  margin-top: 1rem;
  padding: 0.75rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.3);
  background: rgba(2, 6, 23, 0.85);
  color: #e2e8f0;
  font-family: "Fira Code", monospace;
  font-size: 0.9rem;
}

.actions {
  margin-top: 0.75rem;
  display: flex;
  gap: 0.75rem;
}

button {
  padding: 0.5rem 1rem;
  border-radius: 8px;
  border: none;
  background: linear-gradient(120deg, #38bdf8, #818cf8);
  color: #0f172a;
  font-weight: 600;
  cursor: pointer;
}

button.ghost {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.4);
  color: #e2e8f0;
}

button.danger {
  background: transparent;
  border: 1px solid rgba(248, 113, 113, 0.5);
  color: #f87171;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.empty {
  margin-top: 1rem;
  color: #94a3b8;
  font-size: 0.95rem;
}

.feedback {
  font-size: 0.85rem;
  margin-top: 0.5rem;
}

.feedback.success {
  color: #22c55e;
}

.feedback.error {
  color: #f87171;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.version {
  color: #a5b4fc;
  font-size: 0.9rem;
}
</style>
