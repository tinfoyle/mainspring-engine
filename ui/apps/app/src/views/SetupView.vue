<script setup lang="ts">
import {
  APIProblem, beginMFAEnrollment, completeMFAEnrollment, emitAnalytics, getPasskeys, getPrivacyConsent,
  getRecoveryCodeStatus, getSecurityPosture, rotateRecoveryCodes, type MFAChallenge, type MFAKind,
  type RecoveryCodeStatus, type SecurityPosture
} from "@spyglass/api";
import { IoButton, IoLogo } from "@spyglass/design-system";
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useSessionStore } from "../stores/session";
import { registerPasskey } from "../webauthn";

type SetupChoice = "" | "passkey" | MFAKind;
const route = useRoute(); const router = useRouter(); const session = useSessionStore();
const posture = ref<SecurityPosture>(); const recovery = ref<RecoveryCodeStatus>();
const choice = ref<SetupChoice>(""); const passkeyName = ref("My device"); const passkeyCount = ref(0);
const phone = ref(""); const challenge = ref<MFAChallenge>(); const code = ref("");
const newCodes = ref<ReadonlyArray<string>>([]); const savedCodes = ref(false);
const loading = ref(true); const saving = ref(false); const error = ref(""); const announcement = ref("");
const step = computed(() => {
  if (!choice.value) return 1;
  if (choice.value === "passkey") return passkeyCount.value > 0 ? 3 : 2;
  return challenge.value ? 3 : 2;
});

function problem(cause: unknown, fallback: string): string { return cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : fallback; }
function returnTo(): string { const raw = Array.isArray(route.query.return_to) ? route.query.return_to[0] : route.query.return_to; return typeof raw === "string" && raw.startsWith("/") && !raw.startsWith("//") && !raw.startsWith("/app/setup") ? raw : "/app/your-turn"; }
async function recordCompletion(method: string): Promise<void> { try { const consent = await getPrivacyConsent(); await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, { name: "security_enrollment_completed", fields: { method } }); } catch { /* Optional measurement never interrupts setup. */ } }
async function finish(method?: string): Promise<void> { if (method) await recordCompletion(method); await session.load(); await router.replace(returnTo()); }

async function load(): Promise<void> {
  loading.value = true; error.value = "";
  try {
    const [postureValue, passkeyValue, recoveryValue] = await Promise.all([getSecurityPosture(), getPasskeys(), getRecoveryCodeStatus()]);
    posture.value = postureValue; passkeyCount.value = passkeyValue.passkeys.length; recovery.value = recoveryValue;
    if (postureValue.owner_ready) await finish(); else if (passkeyCount.value > 0) choice.value = "passkey";
  } catch (cause) { error.value = problem(cause, "Account setup is unavailable right now."); }
  finally { loading.value = false; }
}

function choose(value: Exclude<SetupChoice, "">): void { choice.value = value; challenge.value = undefined; code.value = ""; error.value = ""; }
function changeChoice(): void { choice.value = ""; challenge.value = undefined; code.value = ""; error.value = ""; }
async function addPasskey(): Promise<void> {
  if (saving.value) return; saving.value = true; error.value = "";
  try { await registerPasskey(passkeyName.value.trim()); passkeyCount.value += 1; passkeyName.value = "My device"; announcement.value = "Passkey added. Next, save your recovery codes."; }
  catch (cause) { error.value = problem(cause, "The passkey could not be added."); }
  finally { saving.value = false; }
}
async function sendCode(): Promise<void> {
  if (saving.value || (choice.value !== "sms" && choice.value !== "email")) return; saving.value = true; error.value = "";
  try { challenge.value = await beginMFAEnrollment(choice.value, choice.value === "sms" ? phone.value.trim() : undefined); code.value = challenge.value.development_code ?? ""; announcement.value = `A code was sent to ${challenge.value.destination_hint}.`; }
  catch (cause) { error.value = problem(cause, "The security code could not be sent."); }
  finally { saving.value = false; }
}
async function verifyCode(): Promise<void> {
  if (saving.value || !challenge.value) return; saving.value = true; error.value = "";
  try { const method = await completeMFAEnrollment(challenge.value.challenge_id, code.value.trim()); announcement.value = "Two-factor authentication is ready."; await finish(method.kind === "sms" ? "sms_code" : "email_code"); }
  catch (cause) { error.value = problem(cause, "That code could not be verified."); }
  finally { saving.value = false; }
}
async function createCodes(): Promise<void> {
  if (saving.value) return; saving.value = true; error.value = "";
  try { const result = await rotateRecoveryCodes(); recovery.value = result.status; posture.value = { owner_ready: passkeyCount.value > 0 && result.status.remaining > 0, passkey_count: passkeyCount.value, recovery_codes_configured: result.status.configured, recovery_codes_remaining: result.status.remaining, mfa_method_count: posture.value?.mfa_method_count ?? 0 }; newCodes.value = result.codes; savedCodes.value = false; announcement.value = "Recovery codes created. Save every code before you finish."; }
  catch (cause) { error.value = problem(cause, "Recovery codes could not be created."); }
  finally { saving.value = false; }
}
async function completePasskeySetup(): Promise<void> {
  if (!savedCodes.value || !posture.value?.owner_ready || saving.value) return; saving.value = true; error.value = "";
  try { await finish("passkey_recovery_codes"); }
  catch (cause) { error.value = problem(cause, "Setup is complete, but Spyglass could not open your Account. Try again."); }
  finally { saving.value = false; }
}
void load();
</script>

<template>
  <section class="setup-page">
    <div class="setup-shell">
      <div class="setup-brand" aria-label="Infinite Ocean Spyglass"><IoLogo /></div>
      <div class="setup-progress" aria-label="Setup progress"><span>Account setup</span><strong>Step {{ step }} of 3</strong></div>
      <div class="setup-card">
        <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
        <div v-if="loading" class="setup-state" role="status"><span class="setup-spinner" aria-hidden="true" /><h1>Getting setup ready…</h1></div>
        <div v-else-if="error && !posture" class="setup-state" role="alert"><p class="eyebrow">Something got in the way</p><h1>We could not load account setup</h1><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></div>
        <template v-else>
          <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>

          <section v-if="!choice" class="setup-step">
            <div class="setup-step-number" aria-hidden="true">1</div><p class="eyebrow">Protect your account</p>
            <h1>How would you like to set up two-factor authentication?</h1>
            <p class="setup-lead">This extra check is required for account owners. Pick the option you will actually have with you when you need it.</p>
            <div class="setup-choice-list">
              <button type="button" class="setup-choice" @click="choose('passkey')"><span class="setup-choice-icon" aria-hidden="true">◇</span><span><strong>Passkey <em>Recommended</em></strong><small>Use your phone or computer’s fingerprint, face, or PIN. Fastest and hardest to steal.</small></span></button>
              <button type="button" class="setup-choice" @click="choose('sms')"><span class="setup-choice-icon" aria-hidden="true">#</span><span><strong>Text message</strong><small>We will send a 6-digit code to your mobile phone when you need to confirm it is you.</small></span></button>
              <button type="button" class="setup-choice" @click="choose('email')"><span class="setup-choice-icon" aria-hidden="true">@</span><span><strong>Email code <em class="setup-choice-caution">Not recommended</em></strong><small>We will send a 6-digit code to your verified email. Easier, but weaker if someone gets into your mailbox.</small></span></button>
            </div>
          </section>

          <form v-else-if="choice === 'passkey' && step === 2" class="setup-step" @submit.prevent="addPasskey">
            <div class="setup-step-number" aria-hidden="true">2</div><p class="eyebrow">Passkey</p><h1>Add this device</h1>
            <p class="setup-lead">Your phone or computer will use its normal fingerprint, face, PIN, or security-key prompt. You can still sign in with Google or your password.</p>
            <label>Name this device<input v-model="passkeyName" autocomplete="off" minlength="2" maxlength="80" placeholder="Work phone or office laptop" required></label>
            <IoButton type="submit" :disabled="saving">{{ saving ? "Waiting for your device…" : "Add passkey" }}</IoButton>
            <button type="button" class="setup-text-action" @click="changeChoice">Choose a different method</button>
          </form>

          <section v-else-if="choice === 'passkey'" class="setup-step">
            <div class="setup-step-number" aria-hidden="true">3</div><p class="eyebrow">Your way back in</p><h1>Save recovery codes</h1>
            <p class="setup-lead">These one-time codes get you back in if you lose every device with your passkey. Store them somewhere separate.</p>
            <template v-if="newCodes.length"><div class="setup-code-panel" role="region" aria-label="New recovery codes"><code v-for="savedCode in newCodes" :key="savedCode">{{ savedCode }}</code></div><p class="setup-code-warning"><strong>This is the only time Spyglass can show these codes.</strong></p><label class="setup-check"><input v-model="savedCodes" type="checkbox"><span>I saved every recovery code somewhere safe.</span></label><IoButton :disabled="saving || !savedCodes" @click="completePasskeySetup">{{ saving ? "Finishing…" : "Finish setup" }}</IoButton></template>
            <template v-else><div class="setup-explainer"><strong>Your passkey is ready.</strong><span>Create recovery codes to finish protecting the account.</span></div><IoButton :disabled="saving" @click="createCodes">{{ saving ? "Creating codes…" : "Create recovery codes" }}</IoButton></template>
          </section>

          <form v-else-if="!challenge" class="setup-step" @submit.prevent="sendCode">
            <div class="setup-step-number" aria-hidden="true">2</div><p class="eyebrow">{{ choice === 'sms' ? 'Text message' : 'Email code' }}</p>
            <h1>{{ choice === 'sms' ? 'What mobile number should we use?' : 'Send a code to your email' }}</h1>
            <p class="setup-lead">{{ choice === 'sms' ? 'We will send a 6-digit code to confirm the phone is yours. Standard message rates may apply.' : 'We will send a 6-digit code to the verified email you use for Infinite Ocean.' }}</p>
            <label v-if="choice === 'sms'">Mobile number<input v-model="phone" type="tel" autocomplete="tel" maxlength="32" placeholder="(555) 123-4567" required><small>US numbers can be entered normally. For other countries, include the country code.</small></label>
            <div v-else class="setup-explainer"><strong>Use my verified email</strong><span>You will see a masked address after the code is sent.</span></div>
            <IoButton type="submit" :disabled="saving">{{ saving ? "Sending…" : "Send my code" }}</IoButton>
            <button type="button" class="setup-text-action" @click="changeChoice">Choose a different method</button>
          </form>

          <form v-else class="setup-step" @submit.prevent="verifyCode">
            <div class="setup-step-number" aria-hidden="true">3</div><p class="eyebrow">Check your {{ choice === 'sms' ? 'phone' : 'email' }}</p><h1>Enter the 6-digit code</h1>
            <p class="setup-lead">We sent it to {{ challenge.destination_hint }}. The code expires in 10 minutes.</p>
            <p v-if="challenge.development_code" class="setup-dev-code">Local test code: <strong>{{ challenge.development_code }}</strong></p>
            <label>Security code<input v-model="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" placeholder="000000" required></label>
            <IoButton type="submit" :disabled="saving || code.length !== 6">{{ saving ? "Checking…" : "Verify and finish" }}</IoButton>
            <button type="button" class="setup-text-action" @click="challenge = undefined; code = ''">Send a new code{{ choice === 'sms' ? ' or change the number' : '' }}</button>
          </form>
        </template>
      </div>
      <p class="setup-help">Need help? Contact Support. We will never ask you to read a security code back to us.</p>
    </div>
  </section>
</template>
