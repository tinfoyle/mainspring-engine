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
  description: "Infinite Ocean is $50 per team each month, with all product packages and configurable provider-neutral AI Token usage.",
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
  <section class="content-hero section-frame"><p class="eyebrow">Simple team pricing</p><h1>One team.<br />The whole system.</h1><p>Infinite Ocean is $50 USD per team each month, before applicable Stripe-calculated tax. There is no stripped-down free tier and no maze of package upgrades.</p></section>
  <section class="pricing-grid section-frame" aria-label="Plan comparison">
    <article v-if="catalogState === 'stale'" class="pricing-unavailable" role="status"><p class="eyebrow">Last verified Catalog</p><h2>Current publication is temporarily delayed</h2><p>These offers were verified from Catalog version {{ catalog?.version }}, published {{ publishedLabel }}. Spyglass revalidates any selection before secure Checkout.</p><button class="button button--secondary" type="button" @click="() => refresh()">Check for current Catalog</button></article>
    <article v-for="offer in paidOffers" :key="offer.code" class="pricing-card--featured">
      <p class="eyebrow">{{ planFor(offer)?.name ?? offer.plan_code }}</p>
      <h2>{{ formatPrice(offer) }}<small>/{{ offer.billing_interval }}</small></h2>
      <p>{{ planFor(offer)?.description }}</p>
      <p><strong>{{ catalog?.ai_token_renewal_grant.quantity.toLocaleString() }} AI Tokens included with each successful renewal.</strong></p>
      <ul class="plan-packages"><li v-for="(mode, packageCode) in planFor(offer)?.packages" :key="packageCode"><span>{{ packageCode }}</span><small>{{ String(mode).replace('_', ' ') }}</small></li></ul>
      <a class="button button--primary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`" @click="chooseOffer($event, offer)">Create your team Account</a>
    </article>
    <article v-if="catalog?.commissioning_offer && launchOffer"><p class="eyebrow">Optional commissioning</p><h2>{{ formatPrice(catalog.commissioning_offer) }}<small> one time</small></h2><p>Hands-on onboarding and commissioning for teams that want guided setup. Choose it during initial Checkout or purchase it once later from Billing. It carries no Affiliate commission and is not part of the recurring subscription.</p><a class="button button--secondary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(launchOffer.code)}`" @click="chooseOffer($event, launchOffer)">Choose during Checkout</a></article>
    <article v-if="catalogError" class="pricing-unavailable" role="status"><p class="eyebrow">Catalog unavailable</p><h2>Checkout is temporarily paused</h2><p>We will not display or submit a price that the application cannot revalidate. No payment attempt has been made.</p><button class="button button--secondary" type="button" @click="() => refresh()">Try Catalog again</button></article>
  </section>
  <section class="pricing-trust section-frame"><h2>What happens after you choose?</h2><ol><li><strong>Create your identity and team shell.</strong><span>No product access is granted until payment succeeds.</span></li><li><strong>Review one clear offer.</strong><span>The owner confirms $50/month, included AI Tokens, optional one-time commissioning and any Affiliate attribution before leaving Infinite Ocean.</span></li><li><strong>Pay securely at Stripe.</strong><span>Stripe calculates applicable tax. Infinite Ocean waits for a signed payment event before enabling the complete product.</span></li></ol></section>
</template>
