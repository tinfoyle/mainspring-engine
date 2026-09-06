import { ref, watch } from "vue";
import { defineStore } from "pinia";
import { useSessionStore } from "./session";
export const useWorkspacePreferences = defineStore("workspace-preferences", () => {
  const session = useSessionStore();
  const pinned = ref<string[]>([]);
  let key = "";
  watch(() => session.userID, id => {
    key = id ? "spyglass.workspace.preferences." + id : "";
    try { const saved: unknown = JSON.parse(localStorage.getItem(key) ?? "[]"); pinned.value = Array.isArray(saved) ? saved.filter(value => ["/app/schedules", "/app/finance", "/app/marketing"].includes(value)) : []; }
    catch { pinned.value = []; }
  }, { immediate: true });
  watch(pinned, value => { if (key) { try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* Preferences remain usable in memory. */ } } }, { deep: true });
  return { pinned };
});
