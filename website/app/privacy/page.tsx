import type { Metadata } from "next";
import { MarketingPage, PageIntro } from "../_components/site-shell";

export const metadata: Metadata = { title: "Privacy Policy" };

export default function PrivacyPage() {
  return <MarketingPage>
    <PageIntro eyebrow="Privacy Policy · Version 1.0 · Effective September 1, 2026" title="Privacy, in plain language." body="We collect the information needed to run and secure Infinite Ocean services. We do not sell personal information, use it for cross-context behavioral advertising, or share SMS consent for marketing." />
    <article className="prose">
      <h2>Who this policy covers</h2>
      <p>This policy applies to Infinite Ocean, LLC websites, applications and services that link to it, including infiniteocean.net, its subdomains and Spyglass. Contact the North Carolina company responsible for this information at <a href="mailto:privacy@infiniteocean.net">privacy@infiniteocean.net</a>.</p>
      <p>For business Customer Content, the customer normally determines why information is processed and Infinite Ocean provides the service on its behalf. We directly control account, billing, security, support and website information.</p>
      <h2>Information and uses</h2>
      <p>We collect account and contact details; authentication, phone and security records; Customer Content deliberately submitted to a service; usage and diagnostic records; Stripe-linked billing and affiliate records; support communications; and optional first-party analytics after consent. We use it to provide, secure, bill, support and improve the services, run requested integrations and AI features, prevent abuse, and meet legal obligations.</p>
      <h2>Analytics and AI</h2>
      <p>Optional public analytics stays off until accepted and excludes names, emails, user or team IDs, payment details, prompts, answers, documents, task content, full URLs, query strings and referring pages. Requested AI work may send the relevant instructions and Customer Content to the configured model provider to produce results, enforce limits and meter usage.</p>
      <h2>Disclosure and mobile information</h2>
      <p>We use providers for hosting, Stripe payments and taxes, identity, email, telecommunications, AI models and integrations selected by the customer. We do not sell personal information or share it for cross-context behavioral advertising.</p>
      <p><strong>Your mobile information will not be sold or shared with third parties or affiliates for promotional or marketing purposes.</strong> SMS opt-in data and consent are disclosed only to carriers, messaging platforms and providers that help deliver and secure the requested text-message service, or as law requires.</p>
      <h2>Retention</h2>
      <p>Raw public analytics is kept for up to 395 days. After Account closure, ordinary Customer Content enters a 30-day recovery period and is then scheduled for deletion. Limited billing, tax, affiliate, security, consent and audit records may remain longer when required; Affiliate financial records may be kept for seven years after final relevant activity.</p>
      <h2>Your rights</h2>
      <p>Depending on location, you may have rights to access, know, correct, export, delete or restrict personal information, object to processing, withdraw consent and complain to a regulator. We do not discriminate for exercising a right. We have not sold or shared personal information as defined by the California Consumer Privacy Act in the preceding 12 months and do not use sensitive information to infer characteristics.</p>
      <h2>International processing, security and children</h2>
      <p>Information may be processed in the United States and other provider locations using a lawful transfer safeguard where required. We use access controls, encryption in transit, strong authentication, least-privilege roles and audit trails, but no service can promise absolute security. The services are for businesses and not directed to children under 18.</p>
      <h2>Contact and changes</h2>
      <p>Submit a request through an available Spyglass privacy control or email <a href="mailto:privacy@infiniteocean.net">privacy@infiniteocean.net</a>. We may verify identity and authority. Material updates will be posted with a new effective date and additional notice where required.</p>
    </article>
  </MarketingPage>;
}
