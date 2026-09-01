<script setup lang="ts">
import { usePublicSeo } from "~/composables/usePublicSeo";

usePublicSeo({
  title: "Terms and Conditions · Infinite Ocean",
  path: "/terms",
  description: "The terms governing Infinite Ocean websites and services, including Spyglass subscriptions, SMS authentication and AI features."
});
</script>

<template>
  <article class="legal-copy section-frame">
    <p class="eyebrow">Terms and Conditions · Version 1.0 · Effective September 1, 2026</p>
    <h1>Terms and Conditions</h1>
    <p class="lede">These terms explain the rules for using Infinite Ocean websites and services, including Spyglass. If you create an account or use a paid service, you agree to them.</p>

    <h2>1. Agreement and authority</h2>
    <p>These Terms and Conditions (“Terms”) are an agreement between you and Infinite Ocean, LLC, a North Carolina limited liability company (“Infinite Ocean,” “we,” “us” or “our”). They apply to websites, applications and services that link to them, including infiniteocean.net, its subdomains and Spyglass (the “Services”).</p>
    <p>You must be at least 18 and able to enter a binding contract. If you use the Services for a company or other organization, you represent that you have authority to bind it. “Customer” means the person or organization responsible for an Account. Additional order forms, program terms or checkout terms may apply; the more specific term controls if there is a direct conflict.</p>

    <h2>2. Accounts and teams</h2>
    <p>You must provide accurate information, keep account access secure and promptly update contact and billing details. You are responsible for activity by users you authorize. Account owners control team membership, roles, integrations and Customer Content. Passkeys, security codes and recovery credentials must not be shared.</p>
    <p>Tell <a href="mailto:security@infiniteocean.net">security@infiniteocean.net</a> promptly if you suspect unauthorized access. We may require strong authentication, suspend risky sessions or take other reasonable steps to protect the Customer, the Services and other users.</p>

    <h2>3. Subscriptions, billing and taxes</h2>
    <p>The current Spyglass team subscription is $50 USD per team each month. An optional commissioning and setup package is $250 USD. Other purchases, including AI Token packages, are shown separately before checkout. Published prices may change prospectively; a price change to an existing subscription will be communicated before it takes effect.</p>
    <p>Subscriptions renew automatically each month until canceled. You authorize Stripe and Infinite Ocean to charge the selected payment method for recurring fees, usage purchases and applicable taxes. Stripe calculates and collects taxes where configured. You may cancel before the next renewal through the available billing controls or Support. Cancellation takes effect at the end of the paid period unless the checkout or an order form says otherwise.</p>
    <p>Refund eligibility is governed by the offer or order terms shown at purchase and applicable law. Failed payments may result in restricted or suspended access. Billing credits and Affiliate earnings are not cash unless a published program term expressly provides a reviewed payout process.</p>

    <h2>4. AI Tokens and model services</h2>
    <p>“AI Tokens” are internal service-usage units, not cryptocurrency, money or an ownership interest. Included allotments, top-up quantities, promotional discounts and the number of AI Tokens consumed by a model may vary by the configuration shown in the Services. We may change model providers, model mappings and future usage rates as technology and upstream costs change. We will apply changes prospectively and show the applicable package or rate before a new purchase or use where required.</p>
    <p>AI Token purchases and grants are tied to the applicable Account, have no cash value, and may not be resold or transferred outside an authorized Account transfer. Usage records maintained by the Services control unless you show a clear error.</p>

    <h2>5. SMS authentication terms</h2>
    <p>If you choose text-message authentication, you agree to receive one-time security codes from Infinite Ocean at the mobile number you provide. Message frequency varies. Message and data rates may apply. Consent to SMS is not a condition of purchase, and you may choose another available authentication method.</p>
    <p>Reply <strong>STOP</strong> to opt out or <strong>HELP</strong> for help. After opting out, you must use another available factor to access the Account. Carriers are not liable for delayed or undelivered messages. You are responsible for providing a number you control and for updating or removing it if control changes. Our <NuxtLink to="/privacy">Privacy Policy</NuxtLink> explains how mobile information and consent are handled.</p>

    <h2>6. Customer Content</h2>
    <p>As between Customer and Infinite Ocean, Customer keeps its rights in information, documents, prompts, instructions and other content submitted to the Services (“Customer Content”). Customer gives Infinite Ocean a limited right to host, copy, transmit, transform and otherwise process Customer Content only to provide, secure, support and improve the Services as these Terms and the Privacy Policy permit.</p>
    <p>Customer represents that it has the rights and lawful basis needed to submit Customer Content and direct its processing. Customer must not submit regulated, restricted or highly sensitive information unless the applicable service documentation and agreement expressly support it.</p>

    <h2>7. AI output and human responsibility</h2>
    <p>AI agents and model outputs can be incomplete, incorrect or unsuitable. The Services may help organize information, suggest actions and perform authorized work, but they do not replace professional judgment. Customer remains responsible for reviewing consequential legal, financial, employment, safety, health, regulatory and business decisions and for configuring appropriate approvals and limits.</p>
    <p>Because machine-generated output may not be unique, other users may receive similar output. Infinite Ocean does not claim ownership of Customer Content or, as between Customer and Infinite Ocean, output generated specifically for Customer, subject to third-party rights and applicable law.</p>

    <h2>8. Acceptable use</h2>
    <p>You may not use the Services to violate law or another person’s rights; send spam or unlawful messages; distribute malware; evade security or usage limits; gain unauthorized access; interfere with service operation; impersonate another person; generate or facilitate fraud, abuse or exploitation; or build a competing model or service by scraping, bulk extracting or reverse engineering protected parts of the Services except where law does not allow that restriction.</p>
    <p>You may not use an agent, integration or automation beyond the authority granted by the relevant Account or third-party system. We may investigate suspected abuse and remove content or suspend access when reasonably necessary to protect people, the Services or legal compliance.</p>

    <h2>9. Integrations and third-party services</h2>
    <p>The Services may connect to Stripe, Google, telecommunications carriers, model providers and other third-party services selected by you or needed to provide a feature. Their terms and privacy practices may also apply. We are not responsible for a third party’s independent service, but we remain responsible for our own obligations and for choosing providers with appropriate protections.</p>

    <h2>10. Infinite Ocean property and feedback</h2>
    <p>The Services, software, designs, documentation, trademarks and related technology are owned by Infinite Ocean or its licensors and are protected by law. Subject to these Terms and payment of applicable fees, we grant Customer a limited, non-exclusive, non-transferable right to use the Services during the applicable subscription.</p>
    <p>If you give us feedback, you allow us to use it without restriction or payment, provided we do not identify you publicly without permission.</p>

    <h2>11. Service changes, suspension and termination</h2>
    <p>We may improve, replace or discontinue features and may use different providers to deliver them. We will provide reasonable notice when a material change significantly reduces a paid service, unless an urgent security, legal or provider issue makes advance notice impractical.</p>
    <p>Either party may terminate as provided by the subscription or order. We may suspend or terminate access for material breach, unlawful use, security risk or nonpayment. When an Account closes, Customer may use available export tools during the stated access period. Ordinary Customer Content enters a 30-day recovery period and is then scheduled for deletion, subject to the limited retention described in the Privacy Policy.</p>

    <h2>12. Confidentiality</h2>
    <p>Each party may receive non-public information that a reasonable person would understand to be confidential. The receiving party will use it only to perform under these Terms, protect it with reasonable care and disclose it only to people and providers who need it and are bound to protect it. This does not cover information lawfully known without restriction, independently developed, publicly available without breach or rightfully received from another source.</p>

    <h2>13. Disclaimers</h2>
    <p>TO THE MAXIMUM EXTENT PERMITTED BY LAW, THE SERVICES ARE PROVIDED “AS IS” AND “AS AVAILABLE.” INFINITE OCEAN DISCLAIMS IMPLIED WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, TITLE AND NON-INFRINGEMENT. WE DO NOT WARRANT THAT THE SERVICES OR AI OUTPUT WILL BE ERROR-FREE, UNINTERRUPTED OR SUITABLE FOR EVERY BUSINESS DECISION. Nothing in these Terms limits a warranty or consumer right that law does not allow us to disclaim.</p>

    <h2>14. Limitation of liability</h2>
    <p>TO THE MAXIMUM EXTENT PERMITTED BY LAW, NEITHER PARTY WILL BE LIABLE FOR INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, EXEMPLARY OR PUNITIVE DAMAGES, OR FOR LOST PROFITS, REVENUE, GOODWILL OR DATA, EVEN IF ADVISED THEY WERE POSSIBLE.</p>
    <p>EXCEPT FOR PAYMENT OBLIGATIONS, A PARTY’S FRAUD, WILLFUL MISCONDUCT, VIOLATION OF THE OTHER PARTY’S INTELLECTUAL PROPERTY RIGHTS, OR LIABILITY THAT LAW CANNOT LIMIT, EACH PARTY’S TOTAL LIABILITY ARISING FROM THE SERVICES WILL NOT EXCEED THE AMOUNT CUSTOMER PAID INFINITE OCEAN FOR THE AFFECTED SERVICES DURING THE 12 MONTHS BEFORE THE EVENT GIVING RISE TO THE CLAIM.</p>

    <h2>15. Indemnity</h2>
    <p>Customer will defend and indemnify Infinite Ocean against a third-party claim arising from Customer Content or Customer’s unlawful or unauthorized use of the Services, except to the extent the claim was caused by Infinite Ocean’s breach, negligence or willful misconduct. We will promptly notify Customer and allow reasonable control of the defense, while retaining the right to participate.</p>

    <h2>16. Governing law and disputes</h2>
    <p>North Carolina law governs these Terms without regard to conflict-of-law rules. The state courts serving Washington County, North Carolina, and the United States District Court for the Eastern District of North Carolina have exclusive jurisdiction, and each party consents to that venue. Before filing a claim, the parties will try in good faith for 30 days to resolve it after written notice. Either party may seek urgent injunctive relief when necessary to protect security, confidentiality or intellectual property.</p>

    <h2>17. General terms</h2>
    <p>Neither party is liable for delay caused by events beyond its reasonable control. You may not assign these Terms without our consent except with a merger, reorganization or sale of substantially all relevant assets; we may assign them as part of such a transaction. If a provision is unenforceable, it will be limited to the minimum extent necessary and the rest remains effective. Failure to enforce a provision is not a waiver. These Terms, the Privacy Policy and applicable order or program terms are the complete agreement about the Services and replace earlier discussions on the same subject.</p>

    <h2>18. Changes and contact</h2>
    <p>We may update these Terms prospectively. We will post the effective date and provide additional notice when a material change requires it. Continued use after the effective date constitutes acceptance where permitted by law; if you do not agree, stop using the Services and cancel before the change applies.</p>
    <p>Questions may be sent to <a href="mailto:legal@infiniteocean.net">legal@infiniteocean.net</a>. Support requests may be sent to <a href="mailto:support@infiniteocean.net">support@infiniteocean.net</a>.</p>
  </article>
</template>
