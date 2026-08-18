# ADR-0001: Product identity and web surfaces

- Status: Accepted
- Date: 2026-08-18
- Owners: Infinite Ocean product and platform engineering

## Context

The prototype was named Mainspring and combined the product experience, customer application, and deployment assumptions under that name. The production system needs a durable company identity, a distinct product identity, a public acquisition surface, and authenticated product surfaces with different security and caching requirements.

## Decision

The company is **Infinite Ocean**. The product is **Spyglass**, and its formal product name is **Infinite Ocean: Spyglass**.

The production surfaces are:

| Origin | Purpose |
|---|---|
| `infiniteocean.net` and `www.infiniteocean.net` | Public company and product website, package education, pricing, legal and support content, and signup/login entry |
| `app.infiniteocean.net` | Authenticated Spyglass application |
| `api.infiniteocean.net` | Identity, Account, Catalog, Billing, and routed product APIs |
| `api.infiniteocean.net/webhooks/stripe` | Signed Stripe event ingress |
| `docs.infiniteocean.net` | Product and integration documentation |
| `status.infiniteocean.net` | Independently available service status |

Public marketing content and private application content may initially share source packages or an edge deployment, but they remain separate security and caching surfaces. Public responses may be CDN cached. Account, authentication, billing, and application responses are private and dynamic. Authentication cookies are host-scoped wherever possible; the marketing origin does not receive a broadly scoped application session.

New production code, executable names, environment variables, images, UI, metadata, and documentation use Spyglass naming. The Go module is `github.com/tinfoyle/spyglass-engine`, the production command is `spyglass`, and runtime configuration uses the `SPYGLASS_` prefix.

The word Mainspring may remain only in immutable history and in explicit descriptions of the preserved prototype under `prototype/`. It is not a production-facing alias.

## Consequences

- A visitor can understand Infinite Ocean and Spyglass before creating an identity.
- Product/package pages can evolve and roll back independently from private application behavior.
- Signup and login hand off only to allowlisted Infinite Ocean origins.
- Marketing analytics must not receive customer business content or private application identifiers.
- DNS, TLS, CSP, CORS, cookies, redirects, cache policy, email links, and OAuth callbacks are tested per origin rather than treated as one website.
- Renaming the historical Git repository or local checkout is operational housekeeping, not a runtime compatibility mechanism; imports and production artifacts already use Spyglass.

## Rejected alternatives

- **Keep Mainspring as a customer-facing alias.** This creates avoidable brand ambiguity and prolongs accidental prototype coupling.
- **Serve marketing and the application as one undifferentiated origin.** This broadens cookie and cache scope and makes public deployment changes unnecessarily risky.
- **Use customer-specific hostnames as account authority.** Hostnames and slugs are presentation aids; authenticated Account context remains explicit.

## Verification

- A repository check rejects production-facing Mainspring references outside the prototype and historical rewrite context.
- Rendered-page tests verify Infinite Ocean: Spyglass naming, canonical origins, metadata, navigation, and privacy boundaries.
- Deployment tests verify host-scoped cookies, exact redirect allowlists, cache headers, CSP/CORS rules, and no private data on public routes.
