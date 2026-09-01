<script setup lang="ts">
import { usePublicSeo } from "~/composables/usePublicSeo";

usePublicSeo({
  title: "SMS security consent · Infinite Ocean",
  path: "/sms-consent",
  description: "How a Spyglass user chooses and consents to receive one-time security codes by text message."
});
</script>

<template>
  <article class="sms-evidence section-frame">
    <header>
      <p class="eyebrow">Spyglass account security</p>
      <h1>Text messages are optional and only used for security codes.</h1>
      <p class="lede">A user can choose a passkey, a text message, or an email code during account setup. Choosing text messages does not subscribe the user to marketing.</p>
    </header>

    <section>
      <div>
        <p class="eyebrow">How a user opts in</p>
        <h2>A separate choice during setup</h2>
        <ol>
          <li>Sign up for or sign in to Spyglass.</li>
          <li>Choose <strong>Text message</strong> from the required two-factor authentication setup.</li>
          <li>Enter a U.S. mobile number.</li>
          <li>Read and check the separate, unchecked consent box.</li>
          <li>Select <strong>Send my code</strong>.</li>
        </ol>
        <p>The button remains unavailable until the user checks the consent box. Passkey and email-code options remain available.</p>
      </div>

      <div class="sms-evidence__screen" aria-label="Example of the Spyglass SMS consent screen">
        <p class="eyebrow">Text message · Step 2 of 3</p>
        <h2>What mobile number should we use?</h2>
        <p>We will send a 6-digit code to confirm the phone is yours.</p>
        <label>US mobile number<input type="tel" value="(555) 123-4567" readonly></label>
        <label class="sms-evidence__consent">
          <input type="checkbox" disabled>
          <span><strong>Send security codes by text</strong>By checking this box, you agree to receive one-time security codes from Infinite Ocean at this number. Message frequency varies. Standard message and data rates may apply. Reply STOP to opt out or HELP for help. Consent is not a condition of purchase. Your mobile information will not be sold or shared with third parties or affiliates for promotional or marketing purposes.</span>
        </label>
        <button type="button" disabled>Send my code</button>
      </div>
    </section>

    <section class="sms-evidence__details">
      <h2>What users receive</h2>
      <p>Messages contain a one-time Infinite Ocean security code. Message frequency varies with sign-in and security activity. Standard message and data rates may apply. Reply <strong>STOP</strong> to opt out or <strong>HELP</strong> for help.</p>
      <p>We do not send marketing through this program. Read our <NuxtLink to="/privacy">Privacy Policy</NuxtLink> and <NuxtLink to="/terms">Terms and Conditions</NuxtLink>, or email <a href="mailto:support@infiniteocean.net">support@infiniteocean.net</a>.</p>
    </section>
  </article>
</template>
