# Corporate site deployment record

## 2026-09-01 corporate replacement

- Source revision: `39add29`
- Release directory: `/opt/infiniteocean/releases/corporate/39add29`
- Active content directory: `/opt/infiniteocean/site`
- Pre-change recovery copy: `/opt/infiniteocean/backups/site.pre-corporate.39add29`
- Release archive SHA-256: `1de3ab4a515135468e6b6ecc7b23a53816c35f5d9443400789d28edd59c0b7a2`

The prototype project portfolio was replaced in place so the existing Caddy
bind mount retained its inode. Public HTTPS verification passed for the home,
Spyglass, company, contact, privacy, terms, SMS-consent, social-preview,
sitemap and robots routes. The privacy and SMS mobile-information disclosure,
Terms STOP/HELP disclosure and separate unchecked SMS-consent description were
verified after deployment.

The separately routed `/shopper/` and `/tacktician/` prototype applications
were removed from corporate navigation, content and the sitemap, but were not
disabled or deleted. Retiring those independent routes is a separate operator
decision.

## 2026-09-01 identity-icon follow-up

- Source revision: `ce44342`
- Release directory: `/opt/infiniteocean/releases/corporate/ce44342`
- Pre-change recovery copy: `/opt/infiniteocean/backups/site.pre-favicon.ce44342`

The established Infinite Ocean wave mark was added as the site favicon and
wired into every page. The full site verifier passed before the follow-up was
copied into the active content directory.

## 2026-09-01 concise-copy pass

- Source revision: `d7b4191`
- Release directory: `/opt/infiniteocean/releases/corporate/d7b4191`
- Pre-change recovery copy: `/opt/infiniteocean/backups/site.pre-copy-pass.d7b4191`
- Release archive SHA-256: `559b744798c4e07318729256c97a34a7430cea6b37177e58485415cb63afb9fa`

The visual system was retained while the corporate, product, company and
contact copy was shortened. The homepage was reduced from five sections to
three, feature lists were removed from the product explanation, and the legal
and SMS disclosures were preserved verbatim. Public HTTPS verification passed
for every changed route and the replacement social-preview image.
