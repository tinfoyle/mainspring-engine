<script setup lang="ts">
import {
  APIProblem, beginContactChange, beginMFAReauthentication, completeMFAReauthentication, compromisePasskey, confirmPassword, consumeRecoveryCode, deletePasskey,
  emitAnalytics, getActiveSessions, getCurrentIdentity, getMCPGrants, getPasskeys, getPrivacyConsent, getRecoveryCodeStatus, getSecurityEvents,
  getMFAMethods, getSecurityPosture, getSupportAccessHistory, renamePasskey, revokeAllSessions, revokeMCPGrant, revokeSession, rotateRecoveryCodes,
  type ActiveSession, type CurrentIdentity, type MFAChallenge, type MFAMethod, type MCPGrant, type PasskeyCredential, type RecoveryCodeStatus,
  type SecurityEvent, type SecurityPosture, type SupportAccessEvent
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref } from "vue";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";
import { registerPasskey, reauthenticateWithPasskey } from "../webauthn";

type DestructiveAction = "delete_passkey" | "compromise_passkey" | "revoke_session" | "revoke_all" | "revoke_grant";
const identity = ref<CurrentIdentity>(); const posture = ref<SecurityPosture>(); const recovery = ref<RecoveryCodeStatus>();
const passkeys = ref<ReadonlyArray<PasskeyCredential>>([]); const sessions = ref<ReadonlyArray<ActiveSession>>([]);
const events = ref<ReadonlyArray<SecurityEvent>>([]); const grants = ref<ReadonlyArray<MCPGrant>>([]);
const supportEvents = ref<ReadonlyArray<SupportAccessEvent>>([]);
const mfaMethods = ref<ReadonlyArray<MFAMethod>>([]); const mfaChallenge = ref<MFAChallenge>(); const mfaCode = ref("");
const loading = ref(true); const saving = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref("");
const password = ref(""); const passkeyName = ref(""); const newEmail = ref(""); const recoveryCode = ref("");
const newCodes = ref<ReadonlyArray<string>>([]); const contactNotice = ref("");
const actionOpen = ref(false); const action = ref<DestructiveAction>("revoke_session"); const targetID = ref(""); const targetName = ref(""); const confirmation = ref("");
const targetCurrent = ref(false);
const sessionStore = useSessionStore();
const renameDirty = ref(false);
const ownerReady = computed(() => posture.value?.owner_ready ?? false);
const returnTo = (() => { const value = new URLSearchParams(window.location.search).get("return_to") ?? ""; return value.startsWith("/") && !value.startsWith("//") ? value : ""; })();
const hasUnsavedSecurityWork = computed(() => actionOpen.value
  || Boolean(password.value || passkeyName.value || newEmail.value || recoveryCode.value)
  || Boolean(mfaChallenge.value || mfaCode.value)
  || renameDirty.value
  || newCodes.value.length > 0);
const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedSecurityWork,
  pending: saving,
  message: "Leave Security? Your entered values or one-time recovery codes may be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Security operation is still in progress. Stay on this page until Spyglass confirms the result."
      : newCodes.value.length
        ? "Navigation canceled. Save every one-time recovery code before leaving this page."
        : "Navigation canceled. Your Security input remains available.";
  }
});

function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Never"; }
function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function problem(cause: unknown, fallback: string): string { return cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : fallback; }
function securityHint(cause: unknown): string {
  if (!(cause instanceof APIProblem)) return "";
  if (["passkey_reauthentication_required", "strong_reauthentication_required"].includes(cause.problem?.code ?? "")) return " Confirm with your passkey, phone, or email above, then try again.";
  if (["reauthentication_required", "password_reauthentication_required"].includes(cause.problem?.code ?? "")) return " Confirm your password above, then try again.";
  return "";
}
async function load(): Promise<void> {
  loading.value = true; error.value = "";
  try {
    const [identityValue, postureValue, passkeyValue, recoveryValue, sessionValue, eventValue, grantValue, mfaValue] = await Promise.all([
      getCurrentIdentity(), getSecurityPosture(), getPasskeys(), getRecoveryCodeStatus(), getActiveSessions(), getSecurityEvents(), getMCPGrants(), getMFAMethods()
    ]);
    identity.value = identityValue; posture.value = postureValue; passkeys.value = passkeyValue.passkeys; recovery.value = recoveryValue;
    sessions.value = sessionValue.sessions; events.value = eventValue.events; grants.value = grantValue.grants; mfaMethods.value = mfaValue.methods;
    supportEvents.value = sessionStore.selectedID ? (await getSupportAccessHistory(sessionStore.selectedID)).events : [];
  } catch (cause) { error.value = problem(cause, "Identity security is unavailable right now."); }
  finally { loading.value = false; }
}
async function run(task: () => Promise<unknown>, success: string, reload = true): Promise<boolean> {
  if (saving.value) return false; saving.value = true; error.value = ""; navigationNotice.value = "";
  try { await task(); announcement.value = success; if (reload) await load(); return true; }
  catch (cause) { error.value = problem(cause, "The security change could not be completed.") + securityHint(cause); return false; }
  finally { saving.value = false; }
}
async function recordCompletedSecurityEnrollment(previouslyReady: boolean): Promise<void> {
  if (previouslyReady || !ownerReady.value) return;
  try {
    const consent = await getPrivacyConsent();
    await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, {
      name: "security_enrollment_completed",
      fields: { method: "passkey_recovery_codes" }
    });
  } catch {
    // Optional measurement never delays or changes identity-security setup.
  }
}
async function submitPassword(): Promise<void> { if (await run(() => confirmPassword(password.value), "Password confirmed for recovery operations.", false)) password.value = ""; }
async function addPasskey(): Promise<void> {
  const previouslyReady = ownerReady.value;
  if (await run(() => registerPasskey(passkeyName.value.trim()), "Passkey added and privileged actions unlocked.")) {
    passkeyName.value = "";
    void recordCompletedSecurityEnrollment(previouslyReady);
    if (returnTo && ownerReady.value) { allowNextNavigation(); window.location.assign(returnTo); }
  }
}
async function confirmPasskey(): Promise<void> { if (await run(reauthenticateWithPasskey, "Passkey confirmed. Privileged actions are unlocked for ten minutes.", false)) { if (returnTo && ownerReady.value) { allowNextNavigation(); window.location.assign(returnTo); } } }
async function sendMFACode(method: MFAMethod): Promise<void> { await run(async () => { mfaChallenge.value = await beginMFAReauthentication(method.id); mfaCode.value = mfaChallenge.value.development_code ?? ""; }, `Security code sent to ${method.destination_hint}.`, false); }
async function confirmMFACode(): Promise<void> { if (!mfaChallenge.value) return; if (await run(() => completeMFAReauthentication(mfaChallenge.value!.challenge_id, mfaCode.value.trim()), "Identity confirmed. Privileged actions are unlocked for ten minutes.", false)) { mfaChallenge.value = undefined; mfaCode.value = ""; if (returnTo && ownerReady.value) { allowNextNavigation(); window.location.assign(returnTo); } } }
async function changeContact(): Promise<void> {
  const email = newEmail.value.trim();
  if (await run(async () => { const result = await beginContactChange(email); contactNotice.value = `Verification sent to ${result.new_email}. Your current email remains active until verification.`; }, "Verified-email change requested.", false)) newEmail.value = "";
}
async function rename(credential: PasskeyCredential, event: Event): Promise<void> {
  const form = event.currentTarget as HTMLFormElement; const data = new FormData(form); const name = String(data.get("name") ?? "").trim();
  if (await run(() => renamePasskey(credential.id, name), `${credential.name} renamed.`)) renameDirty.value = false;
}
async function rotateCodes(): Promise<void> {
  const previouslyReady = ownerReady.value;
  if (await run(async () => { const result = await rotateRecoveryCodes(); newCodes.value = result.codes; recovery.value = result.status; }, "New recovery codes created. Save them now.")) {
    void recordCompletedSecurityEnrollment(previouslyReady);
  }
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
  if (action.value === "revoke_all" || action.value === "compromise_passkey" || (action.value === "revoke_session" && targetCurrent.value)) { allowNextNavigation(); window.location.assign("/login?status=signed_out"); }
}
void load();
</script>

<template>
  <section class="page security-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice && !actionOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading"><p class="eyebrow">Identity security</p><h1>Security follows you</h1><p>Your security methods, recovery options, sessions, and connected applications belong to your Infinite Ocean identity—not to one Account.</p></header>
    <section v-if="loading" class="queue-state" role="status"><h2>Loading identity security…</h2></section>
    <section v-else-if="error && !identity" class="queue-state queue-state--error" role="alert"><h2>Security did not load</h2><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></section>
    <template v-else>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section class="security-posture"><div><p class="eyebrow">Owner readiness</p><h2>{{ ownerReady ? "Identity secured" : "Setup incomplete" }}</h2><p>{{ identity?.primary_email }}</p></div><dl><div><dt>Passkeys</dt><dd>{{ posture?.passkey_count ?? 0 }}</dd></div><div><dt>Code methods</dt><dd>{{ posture?.mfa_method_count ?? 0 }}</dd></div><div><dt>Recovery codes</dt><dd>{{ posture?.recovery_codes_remaining ?? 0 }}</dd></div></dl></section>

      <div class="security-grid">
        <section class="security-card"><h2>Confirm your identity</h2><p>Password confirmation unlocks factor recovery. A passkey, text, or email code unlocks privileged Account changes for ten minutes.</p><form @submit.prevent="submitPassword"><label>Current password<input v-model="password" type="password" autocomplete="current-password" minlength="12" required></label><IoButton type="submit" :disabled="saving">Confirm password</IoButton></form><IoButton v-if="passkeys.length" kind="secondary" :disabled="saving" @click="confirmPasskey">Confirm with a passkey</IoButton><IoButton v-for="method in mfaMethods" :key="method.id" kind="secondary" :disabled="saving" @click="sendMFACode(method)">Send code to {{ method.destination_hint }}</IoButton><form v-if="mfaChallenge" @submit.prevent="confirmMFACode"><p>Enter the 6-digit code sent to {{ mfaChallenge.destination_hint }}.</p><p v-if="mfaChallenge.development_code" class="form-note">Local test code: {{ mfaChallenge.development_code }}</p><label>Security code<input v-model="mfaCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required></label><IoButton type="submit" :disabled="saving || mfaCode.length !== 6">Confirm code</IoButton></form></section>
        <section class="security-card"><h2>Verified contact</h2><p>Current login: <strong>{{ identity?.primary_email }}</strong>. A new mailbox must verify the request; completion signs out every session.</p><form @submit.prevent="changeContact"><label>New email<input v-model="newEmail" type="email" autocomplete="email" maxlength="254" required></label><IoButton type="submit" :disabled="saving">Send verification</IoButton></form><p v-if="contactNotice" class="referral-confirmed" role="status">{{ contactNotice }}</p></section>
      </div>

      <section class="security-section"><header><div><p class="eyebrow">Phishing-resistant factors</p><h2>Passkeys</h2></div><span>{{ passkeys.length }} of 10</span></header><form class="security-add" @submit.prevent="addPasskey"><label>Passkey name<input v-model="passkeyName" minlength="2" maxlength="80" placeholder="Phone, laptop, or security key" required></label><IoButton type="submit" :disabled="saving">Add passkey</IoButton></form><ol class="security-list"><li v-for="credential in passkeys" :key="credential.id"><div><strong>{{ credential.name }}</strong><small>Added {{ date(credential.created_at) }} · Last used {{ date(credential.last_used_at) }}</small><small>{{ credential.backed_up ? "Backed up" : credential.backup_eligible ? "Backup eligible" : "Device-bound" }}</small></div><details><summary>Manage</summary><form @submit.prevent="rename(credential, $event)"><label>New name<input name="name" :value="credential.name" minlength="2" maxlength="80" required @input="renameDirty = true"></label><IoButton type="submit" kind="secondary">Rename</IoButton></form><IoButton kind="secondary" @click="beginAction('delete_passkey', credential.id, credential.name)">Remove</IoButton><IoButton kind="secondary" @click="beginAction('compromise_passkey', credential.id, credential.name)">Report compromised</IoButton></details></li></ol></section>

      <section class="security-section"><header><div><p class="eyebrow">Code-based factors</p><h2>Phone and email</h2></div><span>{{ mfaMethods.length }}</span></header><p>These methods are easier to use than a passkey. Text messages are stronger than email; passkeys remain the safest choice.</p><ol v-if="mfaMethods.length" class="security-list"><li v-for="method in mfaMethods" :key="method.id"><div><strong>{{ method.kind === 'sms' ? 'Text message' : 'Email code' }}</strong><small>{{ method.destination_hint }} · Added {{ date(method.created_at) }}</small></div><IoButton kind="secondary" :disabled="saving" @click="sendMFACode(method)">Send code</IoButton></li></ol><p v-else class="form-note">No phone or email code method is set up.</p></section>

      <section class="security-section"><header><div><p class="eyebrow">Factor recovery</p><h2>One-time recovery codes</h2></div><span>{{ recovery?.remaining ?? 0 }} remaining</span></header><p>Creating a new set immediately revokes every previous code. Codes are displayed once and are never stored in readable form.</p><IoButton :disabled="saving || passkeys.length === 0" @click="rotateCodes">{{ recovery?.configured ? "Replace recovery codes" : "Create recovery codes" }}</IoButton><div v-if="newCodes.length" class="recovery-code-panel" role="region" aria-label="New recovery codes"><code v-for="code in newCodes" :key="code">{{ code }}</code><strong>Save these now. They cannot be shown again.</strong></div><details v-if="recovery?.configured"><summary>Lost every passkey?</summary><p>Confirm your password above, then spend one saved code to unlock replacement-passkey enrollment for ten minutes.</p><form class="security-add" @submit.prevent="useCode"><label>Saved recovery code<input v-model="recoveryCode" autocomplete="one-time-code" required></label><IoButton type="submit" kind="secondary">Use recovery code</IoButton></form></details></section>

      <section class="security-section"><header><div><p class="eyebrow">Active sessions</p><h2>Where you are signed in</h2></div><IoButton kind="secondary" @click="beginAction('revoke_all', '', 'All sessions')">Sign out everywhere</IoButton></header><ol class="security-list"><li v-for="session in sessions" :key="session.id"><div><strong>{{ session.client_label }} <small v-if="session.current">Current session</small></strong><small>Signed in with {{ label(session.authentication_method) }} · Last used {{ date(session.last_seen_at) }}</small><small>Expires {{ date(session.expires_at) }}</small></div><IoButton kind="secondary" @click="beginAction('revoke_session', session.id, session.client_label, session.current)">{{ session.current ? "Sign out" : "Revoke" }}</IoButton></li></ol></section>

      <section class="security-section"><header><div><p class="eyebrow">Connected MCP clients</p><h2>Applications acting as you</h2></div><span>{{ grants.length }}</span></header><p>Each connection uses your current Account memberships and package access. Revocation invalidates its access and refresh credentials.</p><ol v-if="grants.length" class="security-list"><li v-for="grant in grants" :key="grant.grant_id"><div><strong>{{ grant.client_name }}</strong><small>{{ grant.client_id }}</small><small>Connected {{ date(grant.created_at) }} · Last used {{ date(grant.last_used_at) }}</small></div><IoButton kind="secondary" @click="beginAction('revoke_grant', grant.grant_id, grant.client_name)">Revoke</IoButton></li></ol><p v-else class="form-note">No MCP clients are connected.</p></section>

      <section class="security-section"><header><div><p class="eyebrow">Security history</p><h2>Recent identity activity</h2></div><span>{{ events.length }}</span></header><ol class="security-events"><li v-for="event in events" :key="`${event.type}:${event.occurred_at}:${event.session_id ?? ''}`"><div><strong>{{ label(event.type) }}</strong><small>Infinite Ocean identity</small></div><time :datetime="event.occurred_at">{{ date(event.occurred_at) }}</time></li></ol></section>

      <section class="security-section"><header><div><p class="eyebrow">Staff access</p><h2>When Support viewed your details</h2></div><span>{{ supportEvents.length }}</span></header><p>Spyglass records the staff member, support ticket, reason, and time whenever a read-only view of your details in this Account is opened.</p><ol v-if="supportEvents.length" class="security-events"><li v-for="event in supportEvents" :key="event.id"><div><strong>{{ label(event.action) }}</strong><small>{{ event.staff_display_name }} · {{ event.ticket }} · {{ event.reason }}</small></div><time :datetime="event.occurred_at">{{ date(event.occurred_at) }}</time></li></ol><p v-else class="form-note">No staff support access has been recorded for your details in this Account.</p></section>
    </template>

    <div v-if="actionOpen" class="modal-backdrop"><form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="security-action-title" @submit.prevent="submitAction"><h2 id="security-action-title">Confirm security revocation</h2><p>This takes effect immediately. Type <strong>{{ phrase() }}</strong> to confirm.</p><label>Confirmation<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving || confirmation !== phrase()">{{ saving ? "Revoking…" : "Confirm" }}</IoButton></div></form></div>
  </section>
</template>
