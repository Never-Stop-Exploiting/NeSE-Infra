<template>
  <header class="nav">
    <h1>AwD Infra Dashboard</h1>
    <nav class="links">
      <RouterLink to="/">Overview</RouterLink>
      <RouterLink to="/schedule">Schedule</RouterLink>
      <RouterLink to="/challenges">Challenges</RouterLink>
      <RouterLink to="/targets">Targets</RouterLink>
      <RouterLink to="/exploits">Exploits</RouterLink>
      <RouterLink to="/flags">Flags</RouterLink>
      <RouterLink to="/runs">Runs</RouterLink>
      <RouterLink to="/runners">Runners</RouterLink>
    </nav>
    <div class="actions">
      <span class="token-status">Token 已加载</span>
      <button type="button" @click="handleLogout">注销</button>
    </div>
  </header>
</template>

<script setup lang="ts">
import { useRouter } from "vue-router";
import { useAuthStore } from "@/stores/auth";

const router = useRouter();
const auth = useAuthStore();

const handleLogout = () => {
  auth.clearToken();
  router.push("/login");
};
</script>

<style scoped>
.nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.5rem;
  background: rgba(15, 23, 42, 0.8);
  border-bottom: 1px solid rgba(148, 163, 184, 0.2);
  backdrop-filter: blur(10px);
}

h1 {
  font-size: 1.25rem;
  margin: 0;
}

.links {
  display: flex;
  gap: 1rem;
}

a {
  color: #e2e8f0;
  text-decoration: none;
  font-weight: 500;
}

.router-link-active {
  color: #38bdf8;
}

.actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.token-status {
  font-size: 0.85rem;
  color: #a5b4fc;
}

button {
  background: transparent;
  border: 1px solid rgba(148, 163, 184, 0.5);
  color: #e2e8f0;
  padding: 0.35rem 0.75rem;
  border-radius: 6px;
  cursor: pointer;
}

button:hover {
  border-color: #38bdf8;
  color: #38bdf8;
}
</style>
