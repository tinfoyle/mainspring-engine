<script setup lang="ts">
import { type CatalogOffer, type CatalogPlan } from "@spyglass/api";
import { useRuntimeConfig } from "#imports";
import { computed, onMounted } from "vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicCatalog } from "~/composables/usePublicCatalog";
import { usePublicSeo } from "~/composables/usePublicSeo";

const appOrigin = useRuntimeConfig().public.appOrigin;
const analyticsConsent = useAnalyticsConsent();
const { data: catalog, error: catalogError, refresh, state: catalogState, publishedLabel } = await usePublicCatalog();
const paidOffers = computed(() => catalogState.value === "unavailable" || catalogError.value
  ? []
  : (catalog.value?.offers ?? []).filter((offer) => offer.amount_minor > 0 && offer.billing_interval !== "none"));
const launchOffer = computed(() => paidOffers.value[0]);
usePublicSeo({
  title: "Spyglass pricing · Infinite Ocean",
  path: "/pricing",
  description: "Spyglass is $50 per team each month, with the complete product and included AI Token usage.",
  schema: {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "Infinite Ocean: Spyglass",
    url: "https://www.infiniteocean.net/pricing",
    applicationCategory: "BusinessApplication",
    operatingSystem: "Web",
    offers: paidOffers.value.map((offer) => ({
        "@type": "Offer",
        price: (offer.amount_minor / 100).toFixed(2),
        priceCurrency: offer.currency,
        category: `${offer.billing_interval} subscription`,
        url: "https://www.infiniteocean.net/pricing"
      }))
  }
});

function planFor(offer: CatalogOffer): CatalogPlan | undefined {
  return catalog.value?.plans.find((plan) => plan.code === offer.plan_code && plan.version === offer.plan_version);
}

function formatPrice(offer: Pick<CatalogOffer, "amount_minor" | "currency">): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: offer.currency }).format(offer.amount_minor / 100);
}

function formatPackageName(code: string): string {
  return code.split(/[-_]/).map((word) => `${word.charAt(0).toUpperCase()}${word.slice(1)}`).join(" ");
}

function formatPackageMode(mode: string): string {
  if (mode === "enabled") return "Included";
  if (mode === "read_only") return "View only";
  if (mode === "suspended") return "Unavailable";
  return mode.replaceAll("_", " ");
}

async function chooseOffer(event: MouseEvent, offer: CatalogOffer): Promise<void> {
  event.preventDefault();
  const destination = `${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`;
  try {
    await Promise.race([
      Promise.all([
        analyticsConsent.track({ name: "offer_selected", fields: { offer_code: offer.code, route_name: "pricing" } }),
        analyticsConsent.track({ name: "signup_handoff_started", fields: { offer_code: offer.code } })
      ]),
      new Promise((resolve) => window.setTimeout(resolve, 180))
    ]);
  } catch {
    // Optional analytics never interrupts acquisition.
  }
  window.location.assign(destination);
}

onMounted(async () => {
  if (await analyticsConsent.resolve()) {
    await analyticsConsent.track({ name: "pricing_viewed", fields: { route_name: "pricing" } });
  }
});
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">Straightforward pricing</p><h1>$50 a month<br />for your whole team.</h1><p>You get the complete product. There is no free tier and no maze of add-ons. Stripe adds any tax required for your location.</p></section>
  <section class="pricing-grid section-frame" aria-label="Plan comparison">
    <article v-if="catalogState === 'stale'" class="pricing-unavailable" role="status"><p class="eyebrow">Last confirmed price</p><h2>We are having trouble checking for updates</h2><p>This price was last confirmed in plan version {{ catalog?.version }}, published {{ publishedLabel }}. We check it again before checkout.</p><button class="button button--secondary" type="button" @click="() => refresh()">Check again</button></article>
    <article v-for="offer in paidOffers" :key="offer.code" class="pricing-card--featured">
      <p class="eyebrow">{{ planFor(offer)?.name ?? offer.plan_code }}</p>
      <h2>{{ formatPrice(offer) }}<small>/{{ offer.billing_interval }}</small></h2>
      <p>Everything your team needs to keep work organized, use AI agents and stay on top of the business.</p>
      <p><strong>{{ catalog?.ai_token_renewal_grant.quantity.toLocaleString() }} AI Tokens included with each successful renewal.</strong></p>
      <ul class="plan-packages"><li v-for="(mode, packageCode) in planFor(offer)?.packages" :key="packageCode"><span>{{ formatPackageName(String(packageCode)) }}</span><small>{{ formatPackageMode(String(mode)) }}</small></li></ul>
      <a class="button button--primary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`" @click="chooseOffer($event, offer)">Create your team</a>
    </article>
    <article v-if="catalog?.commissioning_offer && launchOffer"><p class="eyebrow">Want help setting it up?</p><h2>{{ formatPrice(catalog.commissioning_offer) }}<small> one time</small></h2><p>We can help you bring in your business information and get Spyglass set up for your team. Add this during your first checkout or buy it later from Billing. It is optional and does not change your monthly plan.</p><a class="button button--secondary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(launchOffer.code)}`" @click="chooseOffer($event, launchOffer)">Add setup help at checkout</a></article>
    <article v-if="catalogError" class="pricing-unavailable" role="status"><p class="eyebrow">Price check unavailable</p><h2>Checkout is paused for now</h2><p>We cannot confirm the current price, so we will not send you to checkout. No payment has been attempted.</p><button class="button button--secondary" type="button" @click="() => refresh()">Try again</button></article>
  </section>
  <section class="pricing-trust section-frame"><h2>What happens next?</h2><ol><li><strong>Create your login and team.</strong><span>You will not be charged yet.</span></li><li><strong>Check the details.</strong><span>Review the $50 monthly price, included AI Tokens, optional setup help and any referral code.</span></li><li><strong>Pay securely through Stripe.</strong><span>Stripe calculates any required tax. Your team gets access after payment is confirmed.</span></li></ol></section>
</template>
