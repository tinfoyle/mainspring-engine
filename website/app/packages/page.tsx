import type { Metadata } from "next";
import Link from "next/link";
import { PageIntro, SiteFooter, SiteHeader } from "../_components/site-shell";
import { packages } from "../data";

export const metadata: Metadata = { title: "Packages", description: "Configure Spyglass with Work, Agents, Finance, Marketing, Knowledge, and Integrations." };

export default function PackagesPage() {
  return <main><SiteHeader /><PageIntro eyebrow="Feature packages" title="A shared operating foundation, shaped for the work you do." body="Packages add coherent sets of capability to an Account. Access, limits, schedules, and agent tools all follow the same entitlement rules." /><section className="content-section shell"><div className="all-packages">{packages.map(item=><article className="package-detail" id={item.code} key={item.code}><div className="package-icon" aria-hidden="true">{item.mark}</div><div><p className="package-kicker">{item.kicker}</p><h2>{item.name}</h2></div><div><p>{item.summary}</p><ul>{item.highlights.map(highlight=><li key={highlight}>{highlight}</li>)}</ul></div></article>)}</div></section><section className="final-cta shell"><p className="eyebrow light">Begin simply</p><h2>Start free. Add packages when they earn their place.</h2><p>Your Account and business memory stay intact as capability changes.</p><div><Link className="button cream" href="/signup">Create a free Account ↗</Link><Link className="button outline-light" href="/pricing">Compare plans</Link></div></section><SiteFooter /></main>;
}
