import type { Metadata } from "next";
import { MarketingPage, PageIntro } from "../_components/site-shell";

export const metadata: Metadata = { title: "Terms and Conditions" };

export default function TermsPage() {
  return <MarketingPage>
    <PageIntro eyebrow="Terms and Conditions · Version 1.0 · Effective September 1, 2026" title="Terms and Conditions" body="These terms govern Infinite Ocean websites and services, including Spyglass subscriptions, SMS authentication and AI features." />
    <article className="prose">
      <h2>Agreement and accounts</h2>
      <p>These terms are an agreement with Infinite Ocean, LLC, a North Carolina limited liability company. You must be at least 18 and authorized to bind any organization you represent. Keep account details accurate, protect authentication credentials and promptly report suspected unauthorized access.</p>
      <h2>Subscriptions and billing</h2>
      <p>The current Spyglass team subscription is $50 USD per team per month and the optional commissioning package is $250 USD. Subscriptions renew monthly until canceled. You authorize Stripe and Infinite Ocean to charge recurring fees, selected purchases and applicable taxes. Other purchases, including AI Token packages, are shown separately before checkout.</p>
      <h2>AI Tokens and output</h2>
      <p>AI Tokens are internal service-usage units, not money or cryptocurrency. Package quantities, discounts, model mappings and future rates may change prospectively as providers and upstream costs change. AI output may be incomplete or wrong; customers remain responsible for qualified review of consequential business, legal, financial, employment, safety and regulatory decisions.</p>
      <h2>SMS authentication</h2>
      <p>If you choose SMS authentication, you agree to receive one-time security codes at the number provided. Message frequency varies and message and data rates may apply. Consent is not a condition of purchase. Reply STOP to opt out or HELP for help, then use another available factor. Carriers are not liable for delayed or undelivered messages.</p>
      <h2>Customer Content and acceptable use</h2>
      <p>Customers keep their rights in submitted content and allow Infinite Ocean to process it only to provide, secure, support and improve the services. Customers must have authority to submit it. Do not use the services unlawfully, send spam, distribute malware, evade security, gain unauthorized access, impersonate others, facilitate fraud or operate an agent beyond granted authority.</p>
      <h2>Service and property</h2>
      <p>Infinite Ocean and its licensors own the service software, designs, documentation and trademarks. A paid customer receives a limited right to use the service during its subscription. Features and providers may change; we will give reasonable notice when a material change significantly reduces a paid service unless an urgent security, legal or provider issue prevents advance notice.</p>
      <h2>Closure and deletion</h2>
      <p>Access may be suspended for material breach, unlawful use, security risk or nonpayment. After Account closure, ordinary Customer Content enters a 30-day recovery period and is then scheduled for deletion, subject to limited legal and audit retention in the Privacy Policy.</p>
      <h2>Disclaimers and liability</h2>
      <p>To the maximum extent permitted by law, the services are provided “as is” and “as available” without implied warranties. Neither party is liable for indirect, special, consequential or punitive damages. Except for obligations that law cannot limit and specified serious misconduct, total liability will not exceed the amount paid for the affected services in the 12 months before the claim.</p>
      <h2>Governing law and contact</h2>
      <p>North Carolina law governs. State courts serving Washington County and the United States District Court for the Eastern District of North Carolina have exclusive jurisdiction. The parties will first try in good faith for 30 days to resolve a dispute after written notice. Contact <a href="mailto:legal@infiniteocean.net">legal@infiniteocean.net</a>.</p>
    </article>
  </MarketingPage>;
}
