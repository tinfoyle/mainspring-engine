# Stage support-view clarity — 2026-09-05

RC.51 is deployed from source d3545848240ba69d1111fc9c43196a9146240cea.
The reviewed release manifest and immutable active checkout are
160875d1dddc57497539f9aeaeb556510dedd145.

## Changes

The team badge now reads **Team open**, keeping its lifecycle meaning separate
from the **Subscription** card. Missing subscription ID and status show
**No subscription**; a known subscription with missing status shows
**Status unavailable**. Paid and other recorded states remain visible.

**AI tokens** shows Available, Used and Set aside for AI work. An empty balance
has a plain-language explanation. Inactive teams with no subscription and no
available or reserved credits see the signup/payment explanation. A zero balance
does not claim that no credits were ever granted. Reserved credits receive a
short explanation when present. **Technical details** holds the entitlement and
catalog versions. The admin guide documents these meanings.

This release changes the operations UI and documentation. It does not change
account data, grants, billing, authentication, API contracts or migrations.

## Verification

- Operations type checking and production build passed; UI lint passed.
- All 11 existing operations unit tests passed.
- All six directory/report browser cases passed in Firefox, desktop Chromium
  and a 360-pixel phone layout.
- The built support view was rendered with synthetic API fixtures in those
  three browser/layout combinations. There was no page overflow and no
  WCAG A/AA violation reported by Axe. Desktop and phone images were reviewed.
- Additional fixture reviews covered a paid account with a spent balance,
  available and reserved credits, and a known subscription missing its status.
  Paid accounts do not show signup guidance; missing status is not presented
  as a missing subscription. Technical details expand successfully.
- All four release images passed the publisher's vulnerability and secret scans.
- Live Stage serves the new admin bundle containing the reviewed copy.
  Anonymous admin session requests remain HTTP 401; public pricing is HTTP 200.
- The active checkout pointer matches the manifest commit above. Stage has
  51 running containers, 50 healthy checks and zero unhealthy checks.
  Temporary registry credentials were removed and SSH sessions were closed.

Authenticated support-view interactions used local fixtures; the live check
verified the deployed assets and unauthenticated denial without opening a new
customer support grant.
