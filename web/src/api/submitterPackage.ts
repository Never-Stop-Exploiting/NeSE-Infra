import { api } from "./client";
import type { ExploitFile } from "@/types";

export interface SubmitterFile extends ExploitFile {}

export async function listSubmitterFiles(): Promise<SubmitterFile[]> {
  const response = await api.get("/api/v1/submitter/package/files");
  return response.data?.files ?? [];
}

export async function uploadSubmitterFile(file: File): Promise<SubmitterFile> {
  const form = new FormData();
  form.append("file", file);
  const response = await api.post("/api/v1/submitter/package/files", form, {
    headers: { "Content-Type": "multipart/form-data" }
  });
  return response.data?.file;
}

export async function deleteSubmitterFile(name: string): Promise<void> {
  await api.delete(`/api/v1/submitter/package/files/${encodeURIComponent(name)}`);
}

export async function downloadSubmitterFile(name: string): Promise<Blob> {
  const response = await api.get(`/api/v1/submitter/package/files/${encodeURIComponent(name)}`, {
    responseType: "blob"
  });
  return response.data;
}

export async function fetchSubmitterScript(): Promise<string> {
  const response = await api.get("/api/v1/submitter/package/script");
  return response.data?.script ?? "";
}

export async function saveSubmitterScript(script: string): Promise<void> {
  await api.post("/api/v1/submitter/package/script", { script });
}

export async function syncSubmitterPackage(): Promise<string> {
  const response = await api.post("/api/v1/submitter/package/sync");
  return response.data?.version ?? "";
}

export async function fetchSubmitterVersion(): Promise<string> {
  const response = await api.get("/api/v1/submitter/package/version");
  return response.data?.version ?? "";
}
