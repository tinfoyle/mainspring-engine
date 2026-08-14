# Mobile-friendly web UI adoption plan

Status: Deferred pending a focused readiness audit  
Last updated: 2026-08-13

## Intent

Mainspring remains one responsive website. The objective is to make the existing
web workspace comfortable for customers who primarily visit from a phone while
preserving the denser, multi-pane experience that is useful on a desktop.

This plan does not introduce a native mobile application, app-store distribution,
a separate mobile backend, mobile-only routes, or an app-installation program.

## Current position

The React V2 workspace already provides a useful base: a persistent application
shell, client-side navigation, shared API contracts, live server updates, and CSS
breakpoints that collapse the major grids. That is enough to improve the website
incrementally. It is not yet evidence that the complete product is comfortable on
real phones.

We will not begin with a navigation rewrite. We will first measure the actual
friction in the owner's most frequent workflows and distinguish responsive defects
from problems that genuinely require a different composition.

## Readiness audit

Test these workflows at 320, 375, 390, 430, 768, and desktop widths:

1. Start and continue a boardroom conversation.
2. Answer a consolidated Your Turn question.
3. Review an approval or completed agent work.
4. Find, open, and update a work item.
5. Upload and attach a document.
6. Navigate between the primary workspace sections without losing state.

Record horizontal overflow, obstructed actions, keyboard interference, scroll
jumps, undersized touch targets, excessive scrolling, and confusing stacked
content. Real-device checks must include current iOS Safari and Android Chrome.

## Low-risk first pass

Before changing the information architecture:

- increase primary touch targets to at least 44 pixels;
- eliminate unintended horizontal page scrolling;
- keep conversation composers usable when the software keyboard is open;
- preserve transcript position, drafts, filters, and selected records;
- stack side panels and dense forms at narrow widths;
- replace wide financial tables with readable narrow-screen cards or intentional
  contained scrolling;
- make every action available without hover; and
- reduce mobile spacing and headings where they consume useful working room.

The same React components, routes, authorization rules, and APIs remain in use.
Compact layouts show one primary column and expandable supporting sections;
desktop layouts may keep persistent sidebars, split panes, bulk actions, keyboard
shortcuts, and denser data presentation.

## Decision gate

After the audit and low-risk pass, reconsider larger layout changes only if the
critical workflows remain uncomfortable. A broader redesign must be justified by
observed workflow failures rather than the assumption that a phone-sized website
must imitate a native application.

The website is ready for wider mobile use when:

- no critical workflow requires a page refresh;
- no page has unintended horizontal scrolling at 320 pixels;
- the software keyboard does not cover the active composer or its send action;
- primary actions are reachable and have adequate touch targets;
- owners do not lose drafts, scroll position, or selections during navigation;
- the six audit workflows pass automated responsive checks; and
- the same workflows pass manual iOS Safari and Android Chrome checks.

## Tentative implementation order

1. Responsive shell, touch targets, overflow, and automated viewport coverage.
2. Boardroom and ticket conversations, including keyboard and scroll behavior.
3. Home and Your Turn.
4. Work queue and ticket details.
5. Documents and finance.
6. Cross-browser verification and final polish.

This work remains deferred until the readiness audit is explicitly scheduled.
