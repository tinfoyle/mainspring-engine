import { APIProblem, requestJSON, type AccountChoice, type AccountChoices } from "@spyglass/api";
import { defineStore } from "pinia";
import { computed, ref } from "vue";

export const useSessionStore = defineStore("session", () => {
  const accounts = ref<ReadonlyArray<AccountChoice>>([]);
  const selectedID = ref<string>();
  const loading = ref(false);
  const unavailable = ref(false);
  const selected = computed(() => accounts.value.find((account) => account.account_id === selectedID.value));

  async function load(): Promise<void> {
    loading.value = true;
    unavailable.value = false;
    try {
      const result = await requestJSON<AccountChoices>("/api/v1/session/accounts");
      accounts.value = result.accounts;
      selectedID.value ??= result.accounts[0]?.account_id;
    } catch (error) {
	  if (error instanceof APIProblem && error.status === 401) {
	    const returnTo = `${window.location.pathname}${window.location.search}`;
	    window.location.assign(`/login?return_to=${encodeURIComponent(returnTo)}`);
	    return;
	  }
      unavailable.value = true;
    } finally {
      loading.value = false;
    }
  }

  return { accounts, selectedID, selected, loading, unavailable, load };
});
