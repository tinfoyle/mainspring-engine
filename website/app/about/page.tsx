import type { Metadata } from "next";
import Link from "next/link";
import { PageIntro, SiteFooter, SiteHeader } from "../_components/site-shell";

export const metadata: Metadata = { title: "Infinite Ocean", description: "Infinite Ocean creates Spyglass: software for businesses that want clarity and coordinated action." };

export default function AboutPage() {
  return <main><SiteHeader /><PageIntro eyebrow="Infinite Ocean" title="Better instruments for navigating a living business." body="Infinite Ocean creates software that helps organizations understand themselves, coordinate people and intelligent systems, and act without losing judgment or context." /><section className="content-section shell"><div className="content-grid"><article className="story-card"><span>OUR VIEW</span><h2>Businesses are living systems.</h2><p>Plans, records, people, obligations, and opportunities constantly affect one another. Useful software should reveal those relationships instead of scattering them across isolated tools.</p></article><article className="story-card"><span>OUR APPROACH</span><h2>Intelligence needs structure.</h2><p>AI becomes more useful when roles, evidence, tools, budgets, approvals, and durable work are explicit. Spyglass supplies that structure while keeping human authority visible.</p></article></div></section><section className="final-cta shell"><p className="eyebrow light">Our first instrument</p><h2>Spyglass helps a business see itself clearly.</h2><p>Explore the product, then create a free Account when you are ready.</p><div><Link className="button cream" href="/product">Explore Spyglass →</Link><Link className="button outline-light" href="mailto:hello@infiniteocean.net">Talk with Infinite Ocean</Link></div></section><SiteFooter /></main>;
}
