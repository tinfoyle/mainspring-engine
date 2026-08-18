"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { publishedPlans, type DisplayPlan } from "@/lib/catalog";
import { plans as fallbackPlans } from "../data";
import { spyglassURL } from "../links";

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
