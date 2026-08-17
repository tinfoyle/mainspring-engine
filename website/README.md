# Infinite Ocean Website

The public website for Infinite Ocean and its first product, Spyglass.

## Routes

- `/` — company/product landing page and Spyglass preview
- `/product` — operating model and product principles
- `/packages` — Work, Agents, Finance, Marketing, Knowledge, and Integrations
- `/pricing` — illustrative launch plans and free-account entry
- `/signup` — handoff to the private Spyglass identity application
- `/about`, `/security`, `/privacy`, `/terms` — company and trust surfaces

The website is intentionally anonymous and stateless. It does not own authentication, Accounts, billing state, or customer business data. Signup completes on `app.infiniteocean.net`; public pricing will ultimately be supplied by the published Catalog API.

## Development

```bash
npm install
npm run dev
npm test
```

The site uses the bundled Vinext/Cloudflare Sites runtime. `.openai/hosting.json` leaves D1 and R2 disabled because the public site has no durable product state.
