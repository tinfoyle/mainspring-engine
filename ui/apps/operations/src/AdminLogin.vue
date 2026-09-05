<script setup lang="ts">
import { APIProblem, adminLoginStatus, beginAdminLogin, setupAdminAuthenticator, verifyAdminAuthenticator, type OperationsSession, type AdminLoginStatus, type AdminSetup } from "@spyglass/api";
import { IoButton, IoLogo } from "@spyglass/design-system";
import { onMounted, ref } from "vue";
import QRCode from "qrcode";
const emit = defineEmits<{ signedIn: [session: OperationsSession] }>();
const status = ref<AdminLoginStatus>();
const setup = ref<AdminSetup>();
const qr = ref("");
const code = ref("");
const recovery = ref(false);
const saved = ref(false);
const recoveryCodes = ref<string[]>([]);
const pendingSession = ref<OperationsSession>();
const busy = ref(false);
const loading = ref(true);
const error = ref("");
function failure(value: unknown): void { error.value = value instanceof Error ? value.message : "Sign-in could not be completed."; }
async function start(): Promise<void> {
  busy.value = true; error.value = "";
  try { window.location.assign((await beginAdminLogin()).url); }
  catch (value) { failure(value); busy.value = false; }
}
async function enroll(): Promise<void> {
  setup.value = await setupAdminAuthenticator();
  qr.value = await QRCode.toDataURL(setup.value.uri, { width: 256, margin: 4, errorCorrectionLevel: "M" });
}
async function verify(): Promise<void> {
  busy.value = true; error.value = "";
  try {
    const result = await verifyAdminAuthenticator(code.value.trim(), recovery.value);
    code.value = "";
    if (result.stage === "enroll") {
      status.value = { stage: "enroll", display_name: status.value?.display_name ?? "" };
      recovery.value = false; await enroll();
    } else if (result.session) {
      setup.value = undefined; qr.value = "";
      if (result.recovery_codes?.length) { recoveryCodes.value = [...result.recovery_codes]; pendingSession.value = result.session; }
      else emit("signedIn", result.session);
    }
  } catch (value) { failure(value); }
  finally { busy.value = false; }
}
function downloadRecoveryCodes(): void {
  const url = URL.createObjectURL(new Blob(["Spyglass Admin recovery codes\nKeep offline. Each code can be used once.\n\n" + recoveryCodes.value.join("\n") + "\n"], { type: "text/plain" }));
  const link = document.createElement("a"); link.href = url; link.download = "spyglass-admin-recovery-codes.txt"; link.click(); URL.revokeObjectURL(url);
}
function finish(): void {
  if (saved.value && pendingSession.value) { const session = pendingSession.value; recoveryCodes.value = []; pendingSession.value = undefined; emit("signedIn", session); }
}
onMounted(async () => {
  try { status.value = await adminLoginStatus(); if (status.value.stage === "enroll") await enroll(); }
  catch (value) { if (!(value instanceof APIProblem && value.status === 401)) failure(value); }
  finally { loading.value = false; }
  if (new URLSearchParams(window.location.search).get("login") === "failed") { error.value = "Google sign-in was not accepted. Use your approved staff account and try again."; window.history.replaceState(null, "", "/"); }
});
</script>

<template>
  <main class="login-shell">
    <section class="login-card" aria-labelledby="login-title">
      <IoLogo />
      <h1 id="login-title">Spyglass Admin</h1>
      <p v-if="loading" role="status">Checking sign-in…</p>
      <template v-else-if="recoveryCodes.length">
        <h2>Save your recovery codes</h2>
        <p>Keep these somewhere safe offline. Each code can be used once to replace a lost authenticator. You will not see this list again.</p>
        <ul class="recovery-list"><li v-for="item in recoveryCodes" :key="item"><code>{{ item }}</code></li></ul>
        <button type="button" class="auth-link" @click="downloadRecoveryCodes">Download recovery codes</button>
        <label class="save-confirmation"><input v-model="saved" type="checkbox" /> I saved my recovery codes.</label>
        <IoButton :disabled="!saved" @click="finish">Open admin</IoButton>
      </template>
      <template v-else-if="status">
        <p>{{ status.display_name }}</p>
        <template v-if="status.stage === 'enroll'">
          <h2>Set up your authenticator</h2>
          <p>Open Google Authenticator on your phone, tap +, then Scan a QR code.</p>
          <img v-if="qr" :src="qr" width="256" height="256" alt="Scan this QR code with your authenticator app" class="auth-qr" />
          <details v-if="setup"><summary>Enter a setup key instead</summary><p>Choose a time-based code and enter this key:</p><code class="setup-key">{{ setup.secret }}</code></details>
        </template>
        <h2 v-else>{{ recovery ? "Use a recovery code" : "Enter your authenticator code" }}</h2>
        <p v-if="status.stage === 'code'">{{ recovery ? "This lets you set up a replacement authenticator before opening admin." : "Use the six-digit code for Spyglass Admin on your phone." }}</p>
        <form @submit.prevent="verify">
          <label for="admin-code">{{ recovery ? "Recovery code" : "Authenticator code" }}</label>
          <input id="admin-code" v-model="code" :inputmode="recovery ? 'text' : 'numeric'" :autocomplete="recovery ? 'off' : 'one-time-code'" :pattern="recovery ? undefined : '[0-9]{6}'" :maxlength="recovery ? 40 : 6" required />
          <IoButton type="submit" :disabled="busy || (status.stage === 'enroll' && !setup)">{{ busy ? "Checking…" : status.stage === 'enroll' ? "Confirm authenticator" : "Continue" }}</IoButton>
        </form>
        <button v-if="status.stage === 'code'" type="button" class="auth-link" :disabled="busy" @click="recovery = !recovery; code = ''; error = ''">{{ recovery ? "Use authenticator instead" : "Lost your authenticator?" }}</button>
        <button type="button" class="auth-link" :disabled="busy" @click="start">Use a different Google account</button>
      </template>
      <template v-else>
        <p>Sign in with your approved Google account, then enter your authenticator code.</p>
        <IoButton :disabled="busy" @click="start">{{ busy ? "Opening Google…" : "Continue with Google" }}</IoButton>
      </template>
      <p v-if="error" class="notice notice--error" role="alert">{{ error }}</p>
    </section>
  </main>
</template>
<style scoped>
.login-card { max-width: 34rem; }
form { display: grid; gap: .8rem; margin-top: 1.2rem; }
input:not([type=checkbox]) { width: 100%; padding: .8rem; font: inherit; border: 1px solid #64748b; border-radius: .4rem; box-sizing: border-box; }
.auth-qr { display: block; max-width: 100%; height: auto; margin: 1rem auto; }
.auth-link { display: block; margin: 1rem 0 0; background: none; border: 0; color: inherit; text-decoration: underline; cursor: pointer; }
.setup-key, .recovery-list code { overflow-wrap: anywhere; }
.recovery-list { padding-left: 1.2rem; line-height: 1.8; }
.save-confirmation { display: flex; gap: .5rem; margin: 1rem 0; }
details { margin: 1rem 0; }
</style>
