<template>
  <div class="login-page">
    <form class="login-card" @submit.prevent="handleSubmit">
      <h2>Control Panel Token</h2>
      <p>
        Enter the operations-provided
        <code>CONTROL_PANEL_TOKEN</code>
        used by the backend for SSO verification.
      </p>
      <label for="token-input">Token</label>
      <input
        id="token-input"
        v-model="tokenInput"
        type="password"
        placeholder="Paste CONTROL_PANEL_TOKEN"
        required
      />
      <button type="submit">Save & Continue</button>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const router = useRouter();
const route = useRoute();
const tokenInput = ref(auth.token);

const handleSubmit = () => {
  auth.setToken(tokenInput.value);
  if (auth.isAuthenticated) {
    const redirect = typeof route.query.redirect === "string" ? route.query.redirect : "/";
    router.push(redirect);
  }
};
</script>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #0b1120;
}

.login-card {
  width: min(420px, 90vw);
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 2rem;
  background: rgba(15, 23, 42, 0.9);
  border-radius: 12px;
  border: 1px solid rgba(148, 163, 184, 0.2);
  color: #e2e8f0;
}

input {
  width: 100%;
  padding: 0.75rem 1rem;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.4);
  background: rgba(15, 23, 42, 0.6);
  color: #f8fafc;
}

button {
  padding: 0.75rem 1rem;
  border-radius: 8px;
  border: none;
  background: linear-gradient(90deg, #0ea5e9, #38bdf8);
  color: #0f172a;
  font-weight: 600;
  cursor: pointer;
}

code {
  background: rgba(148, 163, 184, 0.2);
  padding: 0.1rem 0.25rem;
  border-radius: 4px;
  font-family: "Fira Code", monospace;
  font-size: 0.9rem;
}
</style>
