<script setup lang="ts">
import {
  APIProblem, emitAnalytics, getPasskeys, getPrivacyConsent, getRecoveryCodeStatus,
  getSecurityPosture, rotateRecoveryCodes, type RecoveryCodeStatus, type SecurityPosture
} from "@spyglass/api";
import { IoButton, IoLogo } from "@spyglass/design-system";
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useSessionStore } from "../stores/session";
import { registerPasskey } from "../webauthn";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const posture = ref<SecurityPosture>();
const recovery = ref<RecoveryCodeStatus>();
const passkeyName = ref("My device");
const passkeyCount = ref(0);
const newCodes = ref<ReadonlyArray<string>>([]);
const savedCodes = ref(false);
const loading = ref(true);
const saving = ref(false);
const error = ref("");
const announcement = ref("");
const step = computed(() => passkeyCount.value > 0 ? 2 : 1);

function problem(cause: unknown, fallback: string): string {
  return cause instanceof APIProblem ? cause.message : cause instanceof Error ? cause.message : fallback;
}

function returnTo(): string {
  const raw = Array.isArray(route.query.return_to) ? route.query.return_to[0] : route.query.return_to;
  if (typeof raw !== "string" || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/app/setup")) return "/app/your-turn";
  return raw;
}

async function recordCompletion(): Promise<void> {
  try {
    const consent = await getPrivacyConsent();
    await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, {
      name: "security_enrollment_completed",
      fields: { method: "passkey_recovery_codes" }
    });
  } catch {
    // Optional measurement never interrupts account setup.
  }
}

async function finish(record = true): Promise<void> {
  if (record) await recordCompletion();
  await session.load();
  await router.replace(returnTo());
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    const [postureValue, passkeyValue, recoveryValue] = await Promise.all([
      getSecurityPosture(), getPasskeys(), getRecoveryCodeStatus()
    ]);
    posture.value = postureValue;
    passkeyCount.value = passkeyValue.passkeys.length;
    recovery.value = recoveryValue;
    if (postureValue.owner_ready) await finish(false);
  } catch (cause) {
    error.value = problem(cause, "Account setup is unavailable right now.");
  } finally {
    loading.value = false;
  }
}

async function addPasskey(): Promise<void> {
  if (saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    await registerPasskey(passkeyName.value.trim());
    passkeyCount.value += 1;
    passkeyName.value = "My device";
    announcement.value = "Passkey added. Next, save your recovery codes.";
  } catch (cause) {
    error.value = problem(cause, "The passkey could not be added.");
  } finally {
    saving.value = false;
  }
}

async function createCodes(): Promise<void> {
  if (saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    const result = await rotateRecoveryCodes();
    recovery.value = result.status;
    posture.value = {
      owner_ready: passkeyCount.value > 0 && result.status.remaining > 0,
      passkey_count: passkeyCount.value,
      recovery_codes_configured: result.status.configured,
      recovery_codes_remaining: result.status.remaining
    };
    newCodes.value = result.codes;
    savedCodes.value = false;
    announcement.value = "Recovery codes created. Save every code before you finish.";
  } catch (cause) {
    error.value = problem(cause, "Recovery codes could not be created.");
  } finally {
    saving.value = false;
  }
}

async function completeSetup(): Promise<void> {
  if (!savedCodes.value || !posture.value?.owner_ready || saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    await finish();
  } catch (cause) {
    error.value = problem(cause, "Setup is complete, but Spyglass could not open your Account. Try again.");
  } finally {
    saving.value = false;
  }
}

void load();
</script>

<template>
  <section class="setup-page">
    <div class="setup-shell">
      <div class="setup-brand" aria-label="Infinite Ocean Spyglass"><IoLogo /></div>
      <div class="setup-progress" aria-label="Setup progress">
        <span>Account setup</span>
        <strong>Step {{ step }} of 2</strong>
      </div>

      <div class="setup-card">
        <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
        <div v-if="loading" class="setup-state" role="status">
          <span class="setup-spinner" aria-hidden="true" />
          <h1>Getting setup ready…</h1>
        </div>
        <div v-else-if="error && !posture" class="setup-state" role="alert">
          <p class="eyebrow">Something got in the way</p>
          <h1>We could not load account setup</h1>
          <p>{{ error }}</p>
          <IoButton kind="secondary" @click="load">Try again</IoButton>
        </div>
        <template v-else>
          <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>

          <form v-if="step === 1" class="setup-step" @submit.prevent="addPasskey">
            <div class="setup-step-number" aria-hidden="true">1</div>
            <p class="eyebrow">Protect your account</p>
            <h1>Add a passkey</h1>
            <p class="setup-lead">A passkey uses your phone, computer, or security key to confirm it is really you. It protects owner actions without another password to remember.</p>
            <div class="setup-explainer">
              <strong>You can keep signing in with Google or your password.</strong>
              <span>The passkey is an extra check when you do something sensitive.</span>
            </div>
            <label>
              Name this passkey
              <input v-model="passkeyName" autocomplete="off" minlength="2" maxlength="80" placeholder="Work phone or office laptop" required>
            </label>
            <IoButton type="submit" :disabled="saving">{{ saving ? "Waiting for your device…" : "Add passkey" }}</IoButton>
            <small>Your browser will open its normal fingerprint, face, PIN, or security-key prompt.</small>
          </form>

          <section v-else class="setup-step">
            <div class="setup-step-number" aria-hidden="true">2</div>
            <p class="eyebrow">Your way back in</p>
            <h1>Save recovery codes</h1>
            <p class="setup-lead">If you lose every device that holds your passkey, these one-time codes let you recover the account. Store them somewhere separate from your computer.</p>

            <template v-if="newCodes.length">
              <div class="setup-code-panel" role="region" aria-label="New recovery codes">
                <code v-for="code in newCodes" :key="code">{{ code }}</code>
              </div>
              <p class="setup-code-warning"><strong>This is the only time Spyglass can show these codes.</strong></p>
              <label class="setup-check">
                <input v-model="savedCodes" type="checkbox">
                <span>I saved every recovery code somewhere safe.</span>
              </label>
              <IoButton :disabled="saving || !savedCodes" @click="completeSetup">{{ saving ? "Finishing…" : "Finish setup" }}</IoButton>
            </template>
            <template v-else>
              <div class="setup-explainer">
                <strong>Your passkey is ready.</strong>
                <span>Create a set of recovery codes to finish protecting the account.</span>
              </div>
              <IoButton :disabled="saving" @click="createCodes">{{ saving ? "Creating codes…" : "Create recovery codes" }}</IoButton>
            </template>
          </section>
        </template>
      </div>

      <p class="setup-help">Need help? Contact Support. We cannot see or recreate your recovery codes.</p>
    </div>
  </section>
</template>
