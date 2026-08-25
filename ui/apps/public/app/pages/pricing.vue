<script setup lang="ts">
import { emitAnalytics, getPrivacyConsent, type CatalogOffer, type CatalogPlan, type PublicCatalog } from "@spyglass/api";
import { useFetch, useRuntimeConfig } from "#imports";
import { computed, onMounted } from "vue";
import { useAnalyticsConsent } from "~/composables/useAnalyticsConsent";
import { usePublicSeo } from "~/composables/usePublicSeo";

const appOrigin = useRuntimeConfig().public.appOrigin;
const analyticsConsent = useAnalyticsConsent();
const { data: catalog, error: catalogError, refresh } = await useFetch<PublicCatalog>("/catalog.json", { key: "public-catalog" });
const paidOffers = computed(() => (catalog.value?.offers ?? []).filter((offer) => offer.amount_minor > 0 && offer.billing_interval !== "none"));
usePublicSeo({
  title: "Spyglass pricing · Infinite Ocean",
  path: "/pricing",
  description: "Start Spyglass free and compare current published plans before continuing to secure Stripe Checkout.",
  schema: {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: "Infinite Ocean: Spyglass",
    url: "https://www.infiniteocean.net/pricing",
    applicationCategory: "BusinessApplication",
    operatingSystem: "Web",
    offers: [
      { "@type": "Offer", price: "0", priceCurrency: "USD", description: "Free Account creation", url: "https://www.infiniteocean.net/pricing" },
      ...paidOffers.value.map((offer) => ({
        "@type": "Offer",
        price: (offer.amount_minor / 100).toFixed(2),
        priceCurrency: offer.currency,
        category: `${offer.billing_interval} subscription`,
        url: "https://www.infiniteocean.net/pricing"
      }))
    ]
  }
});

function planFor(offer: CatalogOffer): CatalogPlan | undefined {
  return catalog.value?.plans.find((plan) => plan.code === offer.plan_code && plan.version === offer.plan_version);
}

function formatPrice(offer: CatalogOffer): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: offer.currency }).format(offer.amount_minor / 100);
}

async function chooseOffer(event: MouseEvent, offer: CatalogOffer): Promise<void> {
  event.preventDefault();
  const destination = `${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`;
  try {
    await Promise.race([
      Promise.all([
        emitAnalytics(analyticsConsent.allowed.value, { name: "offer_selected", fields: { offer_code: offer.code, route_name: "pricing" } }),
        emitAnalytics(analyticsConsent.allowed.value, { name: "signup_handoff_started", fields: { offer_code: offer.code } })
      ]),
      new Promise((resolve) => window.setTimeout(resolve, 180))
    ]);
  } catch {
    // Optional analytics never interrupts acquisition.
  }
  window.location.assign(destination);
}

onMounted(async () => {
  try {
    const consent = await getPrivacyConsent();
    analyticsConsent.apply(consent);
    await emitAnalytics(analyticsConsent.allowed.value, { name: "pricing_viewed", fields: { route_name: "pricing" } });
  } catch {
    analyticsConsent.failClosed();
  }
});
</script>

<template>
  <section class="content-hero section-frame"><p class="eyebrow">Published pricing</p><h1>Start free.<br />Upgrade with context.</h1><p>Creating an Account never requires payment. Spyglass revalidates every selected offer before secure Stripe Checkout, so this browser never chooses a provider price.</p></section>
  <section class="pricing-grid section-frame" aria-label="Plan comparison">
    <article><p class="eyebrow">Free</p><h2>$0</h2><p>Build the baseline, organize Work and experience Your Turn before making a purchase decision.</p><ul class="plan-packages"><li>Free Account creation</li><li>Published baseline access</li><li>No payment details required</li></ul><a class="button button--secondary" :href="`${appOrigin}/signup`">Start free</a></article>
    <article v-for="offer in paidOffers" :key="offer.code" class="pricing-card--featured">
      <p class="eyebrow">{{ planFor(offer)?.name ?? offer.plan_code }}</p>
      <h2>{{ formatPrice(offer) }}<small>/{{ offer.billing_interval }}</small></h2>
      <p>{{ planFor(offer)?.description }}</p>
      <ul class="plan-packages"><li v-for="(mode, packageCode) in planFor(offer)?.packages" :key="packageCode"><span>{{ packageCode }}</span><small>{{ String(mode).replace('_', ' ') }}</small></li></ul>
      <a class="button button--primary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`" @click="chooseOffer($event, offer)">Choose {{ planFor(offer)?.name ?? "plan" }}</a>
    </article>
    <article v-if="catalogError" class="pricing-unavailable" role="status"><p class="eyebrow">Catalog unavailable</p><h2>Paid offers are temporarily hidden</h2><p>Free Account creation remains available. We will not show or submit a stale provider price.</p><button class="button button--secondary" type="button" @click="() => refresh()">Try Catalog again</button></article>
  </section>
  <section class="pricing-trust section-frame"><h2>What happens after you choose?</h2><ol><li><strong>Create your free Account.</strong><span>Your selected opaque offer code follows the signup journey.</span></li><li><strong>Review inside Spyglass.</strong><span>An owner confirms the current Catalog offer and any actively applied Affiliate referral.</span></li><li><strong>Pay securely at Stripe.</strong><span>Spyglass waits for a signed payment event before changing access.</span></li></ol></section>
</template>
