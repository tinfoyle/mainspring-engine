import Link from "next/link";
import { ProductPreview } from "./_components/product-preview";
import { SiteFooter, SiteHeader } from "./_components/site-shell";
import { packages } from "./data";

const outcomes = [
  ["01", "Understand the business", "Build a source-attributed operating baseline from the records and people who know the work."],
  ["02", "Turn gaps into work", "Convert missing evidence, stalled decisions, and operating needs into accountable work."],
  ["03", "Deploy governed agents", "Give specialists a precise role, bounded tools, and the business context needed to contribute safely."],
  ["04", "Keep people in control", "Bring consequential decisions and unavailable private knowledge back to the right person."],
] as const;

export default function Home() {
  return (
    <main>
      <SiteHeader />

      <section className="hero shell">
        <div className="hero-copy">
          <p className="eyebrow"><span /> Infinite Ocean: Spyglass</p>
          <h1>See the whole business.<br /><em>Move what matters.</em></h1>
          <p className="hero-lede">
            Spyglass brings your people, work, knowledge, and specialist agents into one operating view—so the business can learn, decide, and move with intention.
          </p>
          <div className="hero-actions">
            <Link className="button primary" href="/signup">Create a free account <span aria-hidden="true">↗</span></Link>
            <Link className="button quiet" href="/product">Explore Spyglass <span aria-hidden="true">→</span></Link>
          </div>
          <div className="hero-proof" aria-label="Product principles">
            <span><i /> Start free</span>
            <span><i /> No card required</span>
            <span><i /> Packages grow with you</span>
          </div>
        </div>
        <ProductPreview />
      </section>

      <section className="signal-band" aria-label="Spyglass summary">
        <div className="shell signal-grid">
          <p>One source of operating truth</p>
          <p>Accountable human + agent work</p>
          <p>Evidence behind every conclusion</p>
          <p>Control at every consequential step</p>
        </div>
      </section>

      <section className="section shell outcome-section">
        <div className="section-heading split-heading">
          <div>
            <p className="eyebrow">The operating loop</p>
            <h2>From scattered signals to coordinated action.</h2>
          </div>
          <p>Spyglass keeps the context behind the work attached to the work itself. Every useful result makes the next decision better.</p>
        </div>
        <div className="outcome-grid">
          {outcomes.map(([number, title, body]) => (
            <article className="outcome-card" key={number}>
              <span>{number}</span>
              <h3>{title}</h3>
              <p>{body}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="section package-section">
        <div className="shell">
          <div className="section-heading split-heading">
            <div>
              <p className="eyebrow">Build your Spyglass</p>
              <h2>One system. The packages you need.</h2>
            </div>
            <p>Begin with a clear view of the business, then add capability without replacing the operating foundation underneath it.</p>
          </div>
          <div className="package-grid">
            {packages.slice(0, 4).map((item) => (
              <article className={`package-card ${item.tone}`} key={item.code}>
                <div className="package-icon" aria-hidden="true">{item.mark}</div>
                <p className="package-kicker">{item.kicker}</p>
                <h3>{item.name}</h3>
                <p>{item.summary}</p>
                <ul>{item.highlights.slice(0, 3).map((highlight) => <li key={highlight}>{highlight}</li>)}</ul>
                <Link href={`/packages#${item.code}`}>Explore {item.name} <span aria-hidden="true">→</span></Link>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className="section shell architecture-story">
        <div className="architecture-copy">
          <p className="eyebrow">Ready when the business is</p>
          <h2>Designed to stay calm as the work grows.</h2>
          <p>Every company has its own Account, people, package access, and data boundary. Underneath, Spyglass shares carefully bounded computing capacity across a resilient platform—keeping the experience responsive without multiplying infrastructure for every customer.</p>
          <dl>
            <div><dt>Account-aware</dt><dd>Your identity can move between businesses without mixing their information.</dd></div>
            <div><dt>Package-aware</dt><dd>Every screen, action, schedule, and agent tool respects the same access rules.</dd></div>
            <div><dt>Load-aware</dt><dd>Capacity grows by workload and demand, with fairness controls for every Account.</dd></div>
          </dl>
        </div>
        <div className="cell-visual" aria-label="Accounts share resilient Spyglass cells while retaining isolated data boundaries">
          <div className="ocean-label">Infinite Ocean platform</div>
          <div className="cell-node cell-a"><span>Cell 01</span><b>38 accounts</b><i /></div>
          <div className="cell-node cell-b"><span>Cell 02</span><b>42 accounts</b><i /></div>
          <div className="cell-node cell-c"><span>Cell 03</span><b>Ready capacity</b><i /></div>
          <div className="account-dots" aria-hidden="true">{Array.from({ length: 18 }).map((_, i) => <i key={i} />)}</div>
        </div>
      </section>

      <section className="final-cta shell">
        <p className="eyebrow light">A clearer operating horizon</p>
        <h2>Give the business a place to think—and a system that can act.</h2>
        <p>Start with a free Spyglass Account. Add packages when the work calls for them.</p>
        <div>
          <Link className="button cream" href="/signup">Create your Account <span aria-hidden="true">↗</span></Link>
          <Link className="button outline-light" href="/pricing">See packages and pricing</Link>
        </div>
      </section>

      <SiteFooter />
    </main>
  );
}
