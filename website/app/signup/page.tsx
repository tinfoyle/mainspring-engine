import type { Metadata } from "next";
import Link from "next/link";
import { SiteFooter, SiteHeader } from "../_components/site-shell";
import { spyglassURL } from "../links";

export const metadata: Metadata = { title: "Create your Account", description: "Create a free Infinite Ocean identity and Spyglass Account." };

export default function SignupPage() {
  return <main><SiteHeader /><section className="signup-layout shell"><div className="signup-copy"><p className="eyebrow"><span /> Start with Spyglass</p><h1>Your first clear view is free.</h1><p>Create one Infinite Ocean identity and a Spyglass Account for your business. You can invite a team, join other Accounts, and add paid packages later.</p><ul><li>No payment information required</li><li>One identity can belong to multiple Accounts</li><li>Package access is explicit and changeable</li><li>Your business data remains isolated</li></ul><p>Already have an identity? <Link href={spyglassURL("/")}>Log in to Spyglass</Link>.</p></div><div className="signup-card"><span>FREE ACCOUNT</span><h2>Create your Infinite Ocean identity</h2><p>Your name, email, and business details are collected only on the private Spyglass application origin—not in public-site URLs, logs, or analytics.</p><Link className="button primary" href={spyglassURL("/signup")}>Continue to secure signup <span aria-hidden="true">→</span></Link><p className="legal">By continuing, you agree to Infinite Ocean&apos;s <Link href="/terms">Terms</Link> and acknowledge the <Link href="/privacy">Privacy Notice</Link>.</p><p className="integration-note"><strong>Secure handoff:</strong> identity verification and Account creation complete on the private Spyglass application origin. Payment information is not requested for the free plan.</p></div></section><SiteFooter /></main>;
}
