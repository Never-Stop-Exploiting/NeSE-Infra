import { defineStore } from "pinia";

const STORAGE_KEY = "awdinfra_admin_token";

export const useAuthStore = defineStore("auth", {
  state: () => ({
    token: ""
  }),
  getters: {
    isAuthenticated: (state) => state.token.trim().length > 0
  },
  actions: {
    initializeFromStorage() {
      if (typeof window === "undefined") {
        this.token = "";
        return;
      }
      const stored = window.localStorage.getItem(STORAGE_KEY);
      this.token = stored ?? "";
    },
    setToken(value: string) {
      const sanitized = value.trim();
      this.token = sanitized;
      if (typeof window === "undefined") {
        return;
      }
      if (sanitized) {
        window.localStorage.setItem(STORAGE_KEY, sanitized);
      } else {
        window.localStorage.removeItem(STORAGE_KEY);
      }
    },
    clearToken() {
      this.setToken("");
    }
  }
});
