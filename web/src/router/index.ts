import { createRouter, createWebHistory } from "vue-router";
import HomeView from "@/views/HomeView.vue";

const routes = [
  {
    path: "/",
    name: "home",
    component: HomeView
  },
  {
    path: "/login",
    name: "login",
    component: () => import("@/views/LoginView.vue")
  },
  {
    path: "/schedule",
    name: "schedule",
    component: () => import("@/views/ScheduleView.vue")
  },
  {
    path: "/challenges",
    name: "challenges",
    component: () => import("@/views/ChallengesView.vue")
  },
  {
    path: "/targets",
    name: "targets",
    component: () => import("@/views/TargetsView.vue")
  },
  {
    path: "/exploits",
    name: "exploits",
    component: () => import("@/views/ExploitsView.vue")
  },
  {
    path: "/flags",
    name: "flags",
    component: () => import("@/views/FlagsView.vue")
  },
  {
    path: "/runners",
    name: "runners",
    component: () => import("@/views/RunnersView.vue")
  },
  {
    path: "/runs",
    name: "runs",
    component: () => import("@/views/RunsView.vue")
  },
  {
    path: "/logs",
    name: "logs",
    component: () => import("@/views/LogsView.vue")
  }
];

const router = createRouter({
  history: createWebHistory(),
  routes
});

export default router;
