<script setup lang="ts">
import {
  APIProblem, beginContactChange, compromisePasskey, confirmPassword, consumeRecoveryCode, deletePasskey,
  getActiveSessions, getCurrentIdentity, getMCPGrants, getPasskeys, getRecoveryCodeStatus, getSecurityEvents,
  getSecurityPosture, renamePasskey, revokeAllSessions, revokeMCPGrant, revokeSession, rotateRecoveryCodes,
  type ActiveSession, type CurrentIdentity, type MCPGrant, type PasskeyCredential, type RecoveryCodeStatus,
  type SecurityEvent, type SecurityPosture
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref } from "vue";
import { registerPasskey, reauthenticateWithPasskey } from "../webauthn";

type DestructiveAction = "delete_passkey" | "compromise_passkey" | "revoke_session" | "revoke_all" | "revoke_grant";
const identity = ref<CurrentIdentity>(); const posture = ref<SecurityPosture>(); const recovery = ref<RecoveryCodeStatus>();
const passkeys = ref<ReadonlyArray<PasskeyCredential>>([]); const sessions = ref<ReadonlyArray<ActiveSession>>([]);
const events = ref<ReadonlyArray<SecurityEvent>>([]); const grants = ref<ReadonlyArray<MCPGrant>>([]);
const loading = ref(true); const saving = ref(false); const error = ref(""); const announcement = ref("");
const password = ref(""); const passkeyName = ref(""); const newEmail = ref(""); const recoveryCode = ref("");
const newCodes = ref<ReadonlyArray<string>>([]); const contactNotice = ref("");
const actionOpen = ref(false); const action = ref<DestructiveAction>("revoke_session"); const targetID = ref(""); const targetName = ref(""); const confirmation = ref("");
const targetCurrent = ref(false);
const ownerReady = computed(() => posture.value?.owner_ready ?? false);
const returnTo = (() => { const value = new URLSearchParams(window.location.search).get("return_to") ?? ""; return value.startsWith("/") && !value.startsWith("//") ? value : ""; })();

function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Never"; }
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function problem(cause: unknown, fallback: string): string { return cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : fallback; }
function securityHint(cause: unknown): string {
  if (!(cause instanceof APIProblem)) return "";
  if (["passkey_reauthentication_required", "strong_reauthentication_required"].includes(cause.problem?.code ?? "")) return " Confirm with a passkey above, then try again.";
  if (["reauthentication_required", "password_reauthentication_required"].includes(cause.problem?.code ?? "")) return " Confirm your password above, then try again.";
  return "";
}
async function load(): Promise<void> {
  loading.value = true; error.value = "";
  try {
    const [identityValue, postureValue, passkeyValue, recoveryValue, sessionValue, eventValue, grantValue] = await Promise.all([
      getCurrentIdentity(), getSecurityPosture(), getPasskeys(), getRecoveryCodeStatus(), getActiveSessions(), getSecurityEvents(), getMCPGrants()
    ]);
    identity.value = identityValue; posture.value = postureValue; passkeys.value = passkeyValue.passkeys; recovery.value = recoveryValue;
    sessions.value = sessionValue.sessions; events.value = eventValue.events; grants.value = grantValue.grants;
  } catch (cause) { error.value = problem(cause, "Identity security is unavailable right now."); }
  finally { loading.value = false; }
}
async function run(task: () => Promise<unknown>, success: string, reload = true): Promise<boolean> {
  if (saving.value) return false; saving.value = true; error.value = "";
  try { await task(); announcement.value = success; if (reload) await load(); return true; }
  catch (cause) { error.value = problem(cause, "The security change could not be completed.") + securityHint(cause); return false; }
  finally { saving.value = false; }
}
async function submitPassword(): Promise<void> { if (await run(() => confirmPassword(password.value), "Password confirmed for recovery operations.", false)) password.value = ""; }
async function addPasskey(): Promise<void> { if (await run(() => registerPasskey(passkeyName.value.trim()), "Passkey added and privileged actions unlocked.")) { passkeyName.value = ""; if (returnTo && ownerReady.value) window.location.assign(returnTo); } }
async function confirmPasskey(): Promise<void> { if (await run(reauthenticateWithPasskey, "Passkey confirmed. Privileged actions are unlocked for ten minutes.", false)) { if (returnTo && ownerReady.value) window.location.assign(returnTo); } }
async function changeContact(): Promise<void> {
  const email = newEmail.value.trim();
  if (await run(async () => { const result = await beginContactChange(email); contactNotice.value = `Verification sent to ${result.new_email}. Your current email remains active until verification.`; }, "Verified-email change requested.", false)) newEmail.value = "";
}
async function rename(credential: PasskeyCredential, event: Event): Promise<void> {
  const form = event.currentTarget as HTMLFormElement; const data = new FormData(form); const name = String(data.get("name") ?? "").trim();
  await run(() => renamePasskey(credential.id, name), `${credential.name} renamed.`);
}
async function rotateCodes(): Promise<void> {
  await run(async () => { const result = await rotateRecoveryCodes(); newCodes.value = result.codes; recovery.value = result.status; }, "New recovery codes created. Save them now.", false);
}
async function useCode(): Promise<void> { if (await run(() => consumeRecoveryCode(recoveryCode.value.trim()), "Recovery code accepted. Add a replacement passkey within ten minutes.")) recoveryCode.value = ""; }
function beginAction(value: DestructiveAction, id = "", name = "", current = false): void { action.value = value; targetID.value = id; targetName.value = name; targetCurrent.value = current; confirmation.value = ""; actionOpen.value = true; }
function phrase(): string { return action.value === "compromise_passkey" ? "COMPROMISED" : action.value === "revoke_all" ? "SIGN OUT" : "REVOKE"; }
async function submitAction(): Promise<void> {
  if (confirmation.value !== phrase()) return;
  const tasks: Record<DestructiveAction, () => Promise<void>> = {
    delete_passkey: () => deletePasskey(targetID.value), compromise_passkey: () => compromisePasskey(targetID.value),
    revoke_session: () => revokeSession(targetID.value), revoke_all: revokeAllSessions, revoke_grant: () => revokeMCPGrant(targetID.value)
  };
  const completed = await run(tasks[action.value], `${targetName.value || "Security access"} revoked.`);
  if (!completed) return; actionOpen.value = false;
  if (action.value === "revoke_all" || action.value === "compromise_passkey" || (action.value === "revoke_session" && targetCurrent.value)) window.location.assign("/login?status=signed_out");
}
void load();
</script>

<template>
  <section class="page security-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <header class="page-heading"><p class="eyebrow">Identity security</p><h1>Security follows you</h1><p>Passkeys, recovery, sessions and connected applications belong to your Infinite Ocean identity—not to one Account.</p></header>
    <section v-if="loading" class="queue-state" role="status"><h2>Loading identity security…</h2></section>
    <section v-else-if="error && !identity" class="queue-state queue-state--error" role="alert"><h2>Security did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></section>
    <template v-else>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section class="security-posture"><div><p class="eyebrow">Owner readiness</p><h2>{{ ownerReady ? "Identity secured" : "Setup incomplete" }}</h2><p>{{ identity?.primary_email }}</p></div><dl><div><dt>Passkeys</dt><dd>{{ posture?.passkey_count ?? 0 }}</dd></div><div><dt>Recovery codes</dt><dd>{{ posture?.recovery_codes_remaining ?? 0 }}</dd></div></dl></section>

      <div class="security-grid">
        <section class="security-card"><h2>Confirm your identity</h2><p>Password confirmation unlocks factor recovery. Passkey confirmation unlocks privileged Account and verified-contact changes for ten minutes.</p><form @submit.prevent="submitPassword"><label>Current password<input v-model="password" type="password" autocomplete="current-password" minlength="12" required></label><IoButton type="submit" :disabled="saving">Confirm password</IoButton></form><IoButton v-if="passkeys.length" kind="secondary" :disabled="saving" @click="confirmPasskey">Confirm with a passkey</IoButton></section>
        <section class="security-card"><h2>Verified contact</h2><p>Current login: <strong>{{ identity?.primary_email }}</strong>. A new mailbox must verify the request; completion signs out every session.</p><form @submit.prevent="changeContact"><label>New email<input v-model="newEmail" type="email" autocomplete="email" maxlength="254" required></label><IoButton type="submit" :disabled="saving">Send verification</IoButton></form><p v-if="contactNotice" class="referral-confirmed" role="status">{{ contactNotice }}</p></section>
      </div>

      <section class="security-section"><header><div><p class="eyebrow">Phishing-resistant factors</p><h2>Passkeys</h2></div><span>{{ passkeys.length }} of 10</span></header><form class="security-add" @submit.prevent="addPasskey"><label>Passkey name<input v-model="passkeyName" minlength="2" maxlength="80" placeholder="Phone, laptop, or security key" required></label><IoButton type="submit" :disabled="saving">Add passkey</IoButton></form><ol class="security-list"><li v-for="credential in passkeys" :key="credential.id"><div><strong>{{ credential.name }}</strong><small>Added {{ date(credential.created_at) }} · Last used {{ date(credential.last_used_at) }}</small><small>{{ credential.backed_up ? "Backed up" : credential.backup_eligible ? "Backup eligible" : "Device-bound" }}</small></div><details><summary>Manage</summary><form @submit.prevent="rename(credential, $event)"><label>New name<input name="name" :value="credential.name" minlength="2" maxlength="80" required></label><IoButton type="submit" kind="secondary">Rename</IoButton></form><IoButton kind="secondary" @click="beginAction('delete_passkey', credential.id, credential.name)">Remove</IoButton><IoButton kind="secondary" @click="beginAction('compromise_passkey', credential.id, credential.name)">Report compromised</IoButton></details></li></ol></section>

      <section class="security-section"><header><div><p class="eyebrow">Factor recovery</p><h2>One-time recovery codes</h2></div><span>{{ recovery?.remaining ?? 0 }} remaining</span></header><p>Creating a new set immediately revokes every previous code. Codes are displayed once and are never stored in readable form.</p><IoButton :disabled="saving || passkeys.length === 0" @click="rotateCodes">{{ recovery?.configured ? "Replace recovery codes" : "Create recovery codes" }}</IoButton><div v-if="newCodes.length" class="recovery-code-panel" role="region" aria-label="New recovery codes"><code v-for="code in newCodes" :key="code">{{ code }}</code><strong>Save these now. They cannot be shown again.</strong></div><details v-if="recovery?.configured"><summary>Lost every passkey?</summary><p>Confirm your password above, then spend one saved code to unlock replacement-passkey enrollment for ten minutes.</p><form class="security-add" @submit.prevent="useCode"><label>Saved recovery code<input v-model="recoveryCode" autocomplete="one-time-code" required></label><IoButton type="submit" kind="secondary">Use recovery code</IoButton></form></details></section>

      <section class="security-section"><header><div><p class="eyebrow">Active sessions</p><h2>Where you are signed in</h2></div><IoButton kind="secondary" @click="beginAction('revoke_all', '', 'All sessions')">Sign out everywhere</IoButton></header><ol class="security-list"><li v-for="session in sessions" :key="session.id"><div><strong>{{ session.client_label }} <small v-if="session.current">Current session</small></strong><small>Signed in with {{ label(session.authentication_method) }} · Last used {{ date(session.last_seen_at) }}</small><small>Expires {{ date(session.expires_at) }}</small></div><IoButton kind="secondary" @click="beginAction('revoke_session', session.id, session.client_label, session.current)">{{ session.current ? "Sign out" : "Revoke" }}</IoButton></li></ol></section>

      <section class="security-section"><header><div><p class="eyebrow">Connected MCP clients</p><h2>Applications acting as you</h2></div><span>{{ grants.length }}</span></header><p>Each connection uses your current Account memberships and package access. Revocation invalidates its access and refresh credentials.</p><ol v-if="grants.length" class="security-list"><li v-for="grant in grants" :key="grant.grant_id"><div><strong>{{ grant.client_name }}</strong><small>{{ grant.client_id }}</small><small>Connected {{ date(grant.created_at) }} · Last used {{ date(grant.last_used_at) }}</small></div><IoButton kind="secondary" @click="beginAction('revoke_grant', grant.grant_id, grant.client_name)">Revoke</IoButton></li></ol><p v-else class="form-note">No MCP clients are connected.</p></section>

      <section class="security-section"><header><div><p class="eyebrow">Security history</p><h2>Recent identity activity</h2></div><span>{{ events.length }}</span></header><ol class="security-events"><li v-for="event in events" :key="`${event.type}:${event.occurred_at}:${event.session_id ?? ''}`"><div><strong>{{ label(event.type) }}</strong><small>Infinite Ocean identity</small></div><time :datetime="event.occurred_at">{{ date(event.occurred_at) }}</time></li></ol></section>
    </template>

    <div v-if="actionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="security-action-title" @submit.prevent="submitAction"><h2 id="security-action-title">Confirm security revocation</h2><p>This takes effect immediately. Type <strong>{{ phrase() }}</strong> to confirm.</p><label>Confirmation<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving || confirmation !== phrase()">{{ saving ? "Revoking…" : "Confirm" }}</IoButton></div></form></div>
  </section>
</template>
