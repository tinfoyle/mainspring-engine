import { APIProblem, requestJSON, type AccountChoice, type AccountChoices, type AccountSelected, type AttentionAccess } from "@spyglass/api";
import { defineStore } from "pinia";
import { computed, ref } from "vue";

export const useSessionStore = defineStore("session", () => {
  const accounts = ref<ReadonlyArray<AccountChoice>>([]);
  const userID = ref<string>();
  const selectedID = ref<string>();
  const contextAccountID = ref<string>();
  const loading = ref(false);
  const selecting = ref(false);
  const unavailable = ref(false);
  const selectionError = ref("");
  const selected = computed(() => accounts.value.find((account) => account.account_id === selectedID.value));
  const attentionAccess = computed<AttentionAccess>(() => {
    const account = selected.value;
    const work = account?.entitlements.packages.find((item) => item.code === "work");
    const agents = account?.entitlements.packages.find((item) => item.code === "agents");
    const participant = account?.role === "owner" || account?.role === "administrator" || account?.role === "member";
    const approver = account?.role === "owner" || account?.role === "administrator";
    return {
      work: Boolean(work && work.mode !== "suspended"),
      workWritable: Boolean(work?.mode === "enabled" && participant),
      approvals: Boolean(agents && agents.mode !== "suspended" && approver),
      approvalsWritable: Boolean(agents?.mode === "enabled" && approver)
    };
  });

  async function load(): Promise<void> {
    loading.value = true;
    unavailable.value = false;
    try {
      const result = await requestJSON<AccountChoices>("/api/v1/session/accounts");
      userID.value = result.user_id;
      accounts.value = result.accounts;
      contextAccountID.value = result.selected_account_id;
      selectedID.value = contextAccountID.value ?? selectedID.value ?? result.accounts[0]?.account_id;
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

  async function select(accountID: string): Promise<void> {
    if (accountID === contextAccountID.value || selecting.value) return;
    selecting.value = true;
    selectionError.value = "";
    try {
      const result = await requestJSON<AccountSelected>("/api/v1/session/account", {
        method: "POST", body: JSON.stringify({ account_id: accountID })
      });
      contextAccountID.value = result.account_context.account_id;
      selectedID.value = result.account_context.account_id;
    } catch (error) {
      selectionError.value = error instanceof APIProblem ? error.message : "That Account could not be selected.";
      throw error;
    } finally {
      selecting.value = false;
    }
  }

  return { accounts, userID, selectedID, selected, attentionAccess, loading, selecting, unavailable, selectionError, load, select };
});
