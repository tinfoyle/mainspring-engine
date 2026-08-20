import type { Metadata } from "next";
import { MarketingPage, PageIntro } from "../_components/site-shell";
import { configuredAppOrigin } from "../links";
import { PricingCards } from "./pricing-cards";

export const metadata: Metadata = { title: "Pricing", description: "Start Spyglass free, then add the packages and capacity your business needs." };

export default function PricingPage() {
  return <MarketingPage><PageIntro eyebrow="Simple starting points" title="Start with clarity. Pay when Spyglass starts carrying real work." body="Every Account begins free. Paid plans add packages, members, and operating capacity; your data remains yours through every change." /><section className="content-section shell"><PricingCards appOrigin={configuredAppOrigin()} /></section></MarketingPage>;
}
