import type { Metadata } from "next";
import Link from "next/link";
import { ProductPreview } from "../_components/product-preview";
import { MarketingPage, PageIntro } from "../_components/site-shell";

export const metadata: Metadata = { title: "Product", description: "How Spyglass turns business knowledge into coordinated, governed action." };

const stories = [
  ["01 / BASELINE", "A business memory with receipts", "Spyglass gathers confirmed facts, records, decisions, and evidence into a memory that stays attributable and correctable. Agents work from what the business knows—not a loose prompt."],
  ["02 / WORK", "Every gap has a next move", "Missing evidence, operating risks, promises, and agent recommendations become durable work with an owner, state, dependencies, and a visible history."],
  ["03 / AGENTS", "Specialists, not autonomous chaos", "Each agent has a versioned role, bounded budget, approved tools, and precise context. Spyglass—not the model—controls delegation, retries, and workflow state."],
  ["04 / ATTENTION", "People stay at the meaningful edge", "Private facts, reviews, and consequential approvals come back to the person with the authority and context to decide. Everything else keeps moving."],
] as const;

export default function ProductPage() {
  return <MarketingPage><PageIntro eyebrow="How Spyglass works" title="An operating system built around context, accountability, and motion." body="Spyglass connects what the business knows to what the business is doing—then gives people and governed agents a shared way to make progress." /><section className="wide-preview shell"><ProductPreview /></section><section className="content-section shell"><div className="content-grid">{stories.map(([eyebrow,title,body])=><article className="story-card" key={eyebrow}><span>{eyebrow}</span><h2>{title}</h2><p>{body}</p></article>)}</div></section><section className="final-cta shell"><p className="eyebrow light">Start with the operating truth</p><h2>Let Spyglass show you what the business needs next.</h2><p>A free Account gives you a first look with no card required.</p><div><Link className="button cream" href="/signup">Create a free Account ↗</Link><Link className="button outline-light" href="/packages">Explore packages</Link></div></section></MarketingPage>;
}
