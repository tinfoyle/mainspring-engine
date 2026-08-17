import type { Metadata } from "next";
import Link from "next/link";
import { PageIntro, SiteFooter, SiteHeader } from "../_components/site-shell";
import { plans } from "../data";

export const metadata: Metadata = { title: "Pricing", description: "Start Spyglass free, then add the packages and capacity your business needs." };

export default function PricingPage() {
  return <main><SiteHeader /><PageIntro eyebrow="Simple starting points" title="Start with clarity. Pay when Spyglass starts carrying real work." body="Every Account begins free. Paid plans add packages, members, and operating capacity; your data remains yours through every change." /><section className="content-section shell"><div className="pricing-grid">{plans.map(plan=><article className={`price-card ${plan.featured?"featured":""}`} key={plan.name}>{plan.featured&&<em>Most useful start</em>}<h2>{plan.name}</h2><div className="price"><strong>{plan.price}</strong><span>{plan.cadence}</span></div><p>{plan.description}</p><ul>{plan.features.map(feature=><li key={feature}>{feature}</li>)}</ul><Link className={`button ${plan.featured?"primary":"quiet"}`} href="/signup">{plan.name==="Free"?"Start free":"Choose "+plan.name}</Link></article>)}</div><p className="pricing-note">Illustrative launch pricing. Published offers and package limits will be shown before purchase. Taxes may apply.</p></section><SiteFooter /></main>;
}
