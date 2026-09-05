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
  return new Intl.NumberFormat("en-US", { style: "currency", currency: offer.currency, minimumFractionDigits: offer.amount_minor % 100 === 0 ? 0 : 2 }).format(offer.amount_minor / 100);
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
  <section class="content-hero pricing-hero section-frame">
    <p class="eyebrow">Pricing</p>
    <h1>One plan for your whole team.</h1>
    <p>Manage tasks, work with AI agents and keep your business information together.</p>
  </section>
  <section class="pricing-grid section-frame" aria-label="Plans and optional setup">
    <article v-if="catalogState === 'stale'" class="pricing-unavailable" role="status"><h2>Showing the last confirmed price</h2><p>Last checked {{ publishedLabel }}. We will confirm the price again before checkout.</p><button class="button button--secondary" type="button" @click="() => refresh()">Check again</button></article>
    <article v-for="offer in paidOffers" :key="offer.code" class="pricing-plan">
      <div class="pricing-plan__purchase">
        <h2>{{ planFor(offer)?.name ?? offer.plan_code }}</h2>
        <p class="pricing-amount">{{ formatPrice(offer) }}<span> / {{ offer.billing_interval }}</span></p>
        <p>One subscription for your team.</p>
        <a class="button button--primary" :href="`${appOrigin}/signup?offer=${encodeURIComponent(offer.code)}`" @click="chooseOffer($event, offer)">Create your team</a>
        <p class="pricing-note">Review your order before paying.<br />Any applicable tax is added at checkout.</p>
      </div>
      <div class="pricing-plan__included">
        <h3>What’s included</h3>
        <ul class="plan-packages"><li v-for="(mode, packageCode) in planFor(offer)?.packages" :key="packageCode"><span>{{ formatPackageName(String(packageCode)) }}</span><small v-if="mode !== 'enabled'">{{ formatPackageMode(String(mode)) }}</small></li></ul>
        <div class="pricing-ai">
          <h3>{{ catalog?.ai_token_renewal_grant.quantity.toLocaleString() }} AI Tokens per renewal</h3>
          <p>AI Tokens pay for your agents’ work. The amount used depends on the task and agent settings.</p>
          <p v-for="bundle in catalog?.ai_token_bundles" :key="bundle.code">Need more? Buy {{ bundle.quantity.toLocaleString() }} AI Tokens for {{ formatPrice(bundle) }} from Billing.</p>
        </div>
      </div>
    </article>
    <article v-if="catalog?.commissioning_offer && launchOffer" class="pricing-setup">
      <div><p class="eyebrow">Optional</p><h2>Help getting started</h2><p>We can help you bring in your business information and set up Spyglass for your team.</p></div>
      <div><p class="pricing-amount">{{ formatPrice(catalog.commissioning_offer) }}<span> one time</span></p><p>Choose setup help at checkout or later in Billing.</p></div>
    </article>
    <article v-if="catalogError || catalogState === 'unavailable'" class="pricing-unavailable" role="status"><h2>We cannot load prices right now</h2><p>Please try again to see the current plan and continue to signup.</p><button class="button button--secondary" type="button" @click="() => refresh()">Try again</button></article>
  </section>
</template>
