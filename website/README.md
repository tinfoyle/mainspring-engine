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

- `SPYGLASS_ENVIRONMENT` — exact `local`, `stage`, or `production` environment name.
- `SPYGLASS_APP_ORIGIN` — exact private application origin; defaults to `https://app.infiniteocean.net`.
- `SPYGLASS_ACCOUNT_API_ORIGIN` — exact account API origin used only by the server-side route to proxy the anonymous published Catalog at `/api/catalog`.

The Catalog proxy caches successful JSON for 60 seconds with five minutes of stale-while-revalidate. If it is unavailable, the page clearly labels its bundled launch figures as illustrative and the application still rejects stale or unpublished offer codes.

## Development

```bash
npm install
npm run dev
npm test
```

The release path is the standalone Node container:

```bash
docker build --target test -t infinite-ocean-website-test .
docker build -t infinite-ocean-website .
docker run --rm -p 3000:3000 \
  -e SPYGLASS_ENVIRONMENT=local \
  -e SPYGLASS_APP_ORIGIN=http://app.infiniteocean.localhost \
  -e SPYGLASS_ACCOUNT_API_ORIGIN=http://host.docker.internal:8080 \
  infinite-ocean-website
```

The website is a normal self-hosted Next.js service. It has no Cloudflare Worker, Sites, D1, R2, Wrangler, or preview-hosting runtime dependency.
