"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { plans as fallbackPlans } from "../data";
import { spyglassURL } from "../links";

type DisplayPlan = {
  name: string;
  offerCode?: string;
  price: string;
  cadence: string;
  description: string;
  featured: boolean;
  features: readonly string[];
};

type PublicCatalog = {
  plans?: Array<{ code?: unknown; name?: unknown; description?: unknown; packages?: unknown }>;
  offers?: Array<{ code?: unknown; plan_code?: unknown; currency?: unknown; amount_minor?: unknown; billing_interval?: unknown; effective_from?: unknown }>;
};

function publishedPlans(value: unknown): DisplayPlan[] | undefined {
  if (!value || typeof value !== "object") return undefined;
  const catalog = value as PublicCatalog;
  if (!Array.isArray(catalog.plans) || !Array.isArray(catalog.offers)) return undefined;
  const planByCode = new Map(catalog.plans.filter((plan) => typeof plan.code === "string").map((plan) => [plan.code as string, plan]));
  const published = catalog.offers.flatMap((offer): DisplayPlan[] => {
    if (typeof offer.code !== "string" || typeof offer.plan_code !== "string" || typeof offer.amount_minor !== "number" || offer.amount_minor < 0 || typeof offer.currency !== "string" || typeof offer.billing_interval !== "string") return [];
    const effective = typeof offer.effective_from === "string" ? Date.parse(offer.effective_from) : Number.NaN;
    if (!Number.isFinite(effective) || effective > Date.now()) return [];
    const plan = planByCode.get(offer.plan_code);
    if (!plan || typeof plan.name !== "string" || typeof plan.description !== "string" || !plan.packages || typeof plan.packages !== "object") return [];
    const price = new Intl.NumberFormat("en-US", { style: "currency", currency: offer.currency, maximumFractionDigits: offer.amount_minor % 100 === 0 ? 0 : 2 }).format(offer.amount_minor / 100);
    const packageNames = Object.keys(plan.packages).map((code) => `${code.charAt(0).toUpperCase()}${code.slice(1)} package`);
    return [{ name: plan.name, offerCode: offer.amount_minor > 0 ? offer.code : undefined, price, cadence: offer.amount_minor > 0 ? `per ${offer.billing_interval}` : "forever", description: plan.description, featured: offer.plan_code === "team", features: packageNames }];
  });
  return published.length > 0 ? published : undefined;
}

export function PricingCards() {
  const [plans, setPlans] = useState<readonly DisplayPlan[]>(fallbackPlans);
  const [live, setLive] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/catalog", { headers: { accept: "application/json" }, signal: controller.signal })
      .then((response) => response.ok ? response.json() : Promise.reject(new Error("catalog unavailable")))
      .then((catalog: unknown) => {
        const next = publishedPlans(catalog);
        if (next) {
          setPlans(next);
          setLive(true);
        }
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, []);

  return <><div className="pricing-grid">{plans.map(plan=><article className={`price-card ${plan.featured?"featured":""}`} key={plan.name}>{plan.featured&&<em>Most useful start</em>}<h2>{plan.name}</h2><div className="price"><strong>{plan.price}</strong><span>{plan.cadence}</span></div><p>{plan.description}</p><ul>{plan.features.map(feature=><li key={feature}>{feature}</li>)}</ul><Link className={`button ${plan.featured?"primary":"quiet"}`} href={spyglassURL("/signup", plan.offerCode)}>{plan.name==="Free"?"Start free":"Choose "+plan.name}</Link></article>)}</div><p className="pricing-note" aria-live="polite">{live ? "Current published Spyglass offers. Final terms are confirmed before Stripe checkout." : "Illustrative launch pricing. Spyglass validates the selected offer against the live published Catalog before Stripe checkout."} Taxes may apply.</p></>;
}
