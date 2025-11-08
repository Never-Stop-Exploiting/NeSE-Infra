import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router";
import { setAuthTokenProvider } from "@/api/client";
import { useAuthStore } from "@/stores/auth";

import "./styles.css";

const app = createApp(App);

const pinia = createPinia();
app.use(pinia);

const authStore = useAuthStore(pinia);
authStore.initializeFromStorage();
setAuthTokenProvider(() => authStore.token);

router.beforeEach((to) => {
  if (!authStore.isAuthenticated && to.path !== "/login") {
    return {
      path: "/login",
      query: { redirect: to.fullPath }
    };
  }
  if (authStore.isAuthenticated && to.path === "/login") {
    return { path: "/" };
  }
  return true;
});

app.use(router);

app.mount("#app");
