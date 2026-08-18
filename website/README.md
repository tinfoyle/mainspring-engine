# Infinite Ocean Website

The public website for Infinite Ocean and its first product, Spyglass.

## Routes

- `/` — company/product landing page and Spyglass preview
- `/product` — operating model and product principles
- `/packages` — Work, Agents, Finance, Marketing, Knowledge, and Integrations
- `/pricing` — illustrative launch plans and free-account entry
- `/signup` — handoff to the private Spyglass identity application
- `/about`, `/security`, `/privacy`, `/terms` — company and trust surfaces

The website is intentionally anonymous and stateless. It does not own authentication, Accounts, billing state, or customer business data. Signup details are collected only on the private Spyglass application origin. The marketing site passes an opaque offer code through a GET handoff; Spyglass validates it against the published Catalog and never accepts price or entitlement claims from the browser.

Production configuration:

- `NEXT_PUBLIC_SPYGLASS_APP_ORIGIN` — exact HTTPS application origin; defaults to `https://app.infiniteocean.net`.
- `SPYGLASS_ACCOUNT_API_ORIGIN` — exact HTTPS account API origin used only by the Worker to proxy the anonymous published Catalog at `/api/catalog`.

The Catalog proxy caches successful JSON for 60 seconds with five minutes of stale-while-revalidate. If it is unavailable, the page clearly labels its bundled launch figures as illustrative and the application still rejects stale or unpublished offer codes.

## Development

```bash
npm install
npm run dev
npm test
```

The site uses the bundled Vinext/Cloudflare Sites runtime. `.openai/hosting.json` leaves D1 and R2 disabled because the public site has no durable product state.
