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
