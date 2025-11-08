<template>
  <div class="layout">
    <nav-bar v-if="auth.isAuthenticated" />
    <main class="content" :class="{ 'content--full': !auth.isAuthenticated }">
      <RouterView />
    </main>
  </div>
</template>

<script setup lang="ts">
import NavBar from "@/components/NavBar.vue";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
</script>

<style scoped>
.layout {
  display: grid;
  min-height: 100vh;
  grid-template-rows: auto 1fr;
  background: #0f172a;
  color: #f8fafc;
}

.content {
  padding: 1.5rem;
  background: #0b1120;
}

.content--full {
  padding: 0;
}
</style>
