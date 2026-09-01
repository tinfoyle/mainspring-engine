# Infinite Ocean corporate site

This directory is the revision-controlled source for the static corporate site
served from `/opt/infiniteocean/site` on the Infinite Ocean VPS. Infinite Ocean
is presented as the parent software company and Spyglass as its commercial
product. Do not restore the retired prototype project portfolio.

The canonical public routes are `/`, `/spyglass/`, `/about/`, `/contact/`,
`/privacy/`, `/terms/` and `/sms-consent/`. Preserve the legal and consent URLs
through future product-site changes.

## Local preview

```bash
python3 -m http.server 4173 --directory deploy/corporate-site/infiniteocean.net
```

## Deployment

Create a timestamped, mode-700 backup of `/opt/infiniteocean/site`, copy this
site into a new directory on the same filesystem, and replace the contents of
the existing bind-mounted directory in place. Do not replace the directory
inode while Caddy is running. Validate Caddy before its configuration is
changed; ordinary content-only updates do not require a Caddy reload.

After deployment, verify every sitemap route over public HTTPS and confirm that
the privacy, terms and SMS-consent text still contains its required disclosures.
