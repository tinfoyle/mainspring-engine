import Link from "next/link";
import type { ReactNode } from "react";
import { spyglassURL } from "../links";

export function Brand() {
  return <Link className="brand" href="/" aria-label="Infinite Ocean home"><span className="brand-mark" aria-hidden="true"><i /></span><span><strong>INFINITE OCEAN</strong><small>SPYGLASS</small></span></Link>;
}

export function SiteHeader() {
  return <header className="site-header"><div className="shell nav-shell"><Brand /><nav aria-label="Primary navigation"><Link href="/product">Product</Link><Link href="/packages">Packages</Link><Link href="/pricing">Pricing</Link><Link href="/about">Infinite Ocean</Link></nav><div className="nav-actions"><Link className="login-link" href={spyglassURL("/")}>Log in</Link><Link className="button small primary" href="/signup">Start free</Link></div><details className="mobile-nav"><summary aria-label="Open navigation menu"><span /><span /></summary><nav aria-label="Mobile navigation"><Link href="/product">Product</Link><Link href="/packages">Packages</Link><Link href="/pricing">Pricing</Link><Link href="/about">Infinite Ocean</Link><Link href="/signup">Start free</Link></nav></details></div></header>;
}

export function SiteFooter() {
  return <footer className="site-footer"><div className="shell footer-grid"><div><Brand /><p>Software for seeing the business clearly and moving it deliberately.</p><small>© 2026 Infinite Ocean. All rights reserved.</small></div><div><strong>Spyglass</strong><Link href="/product">Product</Link><Link href="/packages">Packages</Link><Link href="/pricing">Pricing</Link><Link href="/signup">Create an Account</Link></div><div><strong>Infinite Ocean</strong><Link href="/about">Company</Link><Link href="/security">Security</Link><Link href="/privacy">Privacy</Link><Link href="/terms">Terms</Link></div><div><strong>Follow the horizon</strong><p>Product notes and practical operating ideas, occasionally.</p><a href="mailto:hello@infiniteocean.net">hello@infiniteocean.net</a></div></div></footer>;
}

export function PageIntro({ eyebrow, title, body }: { eyebrow: string; title: string; body: string }) {
  return <section className="page-intro shell"><p className="eyebrow"><span /> {eyebrow}</p><h1>{title}</h1><p>{body}</p></section>;
}

export function MarketingPage({ children }: { children: ReactNode }) {
  return <><a className="skip-link" href="#main-content">Skip to main content</a><SiteHeader /><main id="main-content" tabIndex={-1}>{children}</main><SiteFooter /></>;
}
