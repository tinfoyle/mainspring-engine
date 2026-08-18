"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import type { CatalogOffer, CatalogPlan, PublicCatalog } from "@/lib/generated/api-types";
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

type PricingCatalog = Pick<PublicCatalog, "plans" | "offers">;

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isCatalogPlan(value: unknown): value is CatalogPlan {
  if (!isObject(value) || typeof value.code !== "string" || typeof value.version !== "number" || !Number.isInteger(value.version) || typeof value.name !== "string" || typeof value.description !== "string" || !isObject(value.packages)) return false;
  return Object.values(value.packages).every((mode) => mode === "enabled" || mode === "read_only" || mode === "suspended");
}

function isCatalogOffer(value: unknown): value is CatalogOffer {
  return isObject(value)
    && typeof value.code === "string"
    && typeof value.plan_code === "string"
    && typeof value.plan_version === "number"
    && Number.isInteger(value.plan_version)
    && typeof value.currency === "string"
    && typeof value.amount_minor === "number"
    && Number.isInteger(value.amount_minor)
    && (value.billing_interval === "none" || value.billing_interval === "month" || value.billing_interval === "year")
    && typeof value.effective_from === "string";
}

function isPricingCatalog(value: unknown): value is PricingCatalog {
  return isObject(value)
    && Array.isArray(value.plans)
    && value.plans.every(isCatalogPlan)
    && Array.isArray(value.offers)
    && value.offers.every(isCatalogOffer);
}

function publishedPlans(value: unknown): DisplayPlan[] | undefined {
  if (!isPricingCatalog(value)) return undefined;
  const planByCode = new Map(value.plans.map((plan) => [plan.code, plan]));
  const published = value.offers.flatMap((offer): DisplayPlan[] => {
    if (offer.amount_minor < 0) return [];
    const effective = Date.parse(offer.effective_from);
    if (!Number.isFinite(effective) || effective > Date.now()) return [];
    const plan = planByCode.get(offer.plan_code);
    if (!plan) return [];
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
