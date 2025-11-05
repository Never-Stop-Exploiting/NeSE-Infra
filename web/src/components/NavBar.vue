<template>
  <header class="nav">
    <h1>AwD Infra Dashboard</h1>
    <nav class="links">
      <RouterLink to="/">Overview</RouterLink>
      <RouterLink to="/targets">Targets</RouterLink>
      <RouterLink to="/exploits">Exploits</RouterLink>
      <RouterLink to="/flags">Flags</RouterLink>
      <RouterLink to="/runs">Runs</RouterLink>
      <RouterLink to="/runners">Runners</RouterLink>
      <RouterLink to="/logs">Logs</RouterLink>
    </nav>
  </header>
</template>

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
</style>
