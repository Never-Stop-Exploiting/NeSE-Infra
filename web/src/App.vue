<template>
  <div class="layout">
    <nav-bar />
    <main class="content">
      <RouterView />
    </main>
  </div>
</template>

<script setup lang="ts">
import NavBar from "@/components/NavBar.vue";
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
</style>
