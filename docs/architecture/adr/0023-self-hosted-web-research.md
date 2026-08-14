# ADR-0023: Self-host web research behind the tool broker

Status: Accepted  
Date: 2026-08-11

## Context

Agents need current public information after tenant documents and conversation context are insufficient. Giving model runners unrestricted HTTP or a provider-specific MCP server would bypass Mainspring's capability, audit, tenancy, and citation controls. Web retrieval also processes attacker-controlled URLs and content, creating SSRF and prompt-injection risk.

## Decision

Mainspring exposes provider-neutral `web.search` and `web.read` capabilities through its existing Tool Broker. The first provider is a private, self-hosted Firecrawl deployment. Search returns bounded metadata; page content is fetched only through a separate read call. Both outputs carry stable `web:` citation IDs.

Firecrawl runs with Playwright, SearXNG, Redis, RabbitMQ, and its PostgreSQL queue in Docker. No retrieval service publishes a host port. The tenant runtime and Temporal worker join a private `research-control` network to call the Firecrawl API. Firecrawl dependencies use a separate internal backend network, while only components that retrieve public content join `research-egress`. Ephemeral model runners remain on their existing isolated network and receive only bounded tool results.

Mainspring rejects non-HTTP schemes, URL credentials, non-web ports, local hostnames, and targets resolving to loopback, private, link-local, carrier-grade NAT, documentation, or other non-public address ranges. Firecrawl calls disable TLS-verification bypass, custom request headers, browser actions, and local webhooks. Returned page content is untrusted evidence and never system instruction.

The MCP surface reuses the broker rather than exposing Firecrawl MCP directly. MCP calls receive short-lived signed grants and are audited as the configured tenant user.

## Consequences

- Firecrawl can be replaced without changing agent or MCP contracts.
- Search snippets cannot be represented as fully read sources.
- The platform owns Firecrawl upgrades, persistence, backup, monitoring, proxy quality, capacity, and AGPL compliance review.
- Application URL validation reduces SSRF risk but does not replace host-level egress filtering. Production deployment must block Docker bridge, host, private network, and cloud metadata ranges at the firewall or egress proxy, including across redirects and DNS rebinding.
- Self-hosted Firecrawl lacks some hosted anti-bot capabilities; failures remain explicit instead of silently falling back to unrestricted agent networking.

## Rollout checks

- Run broker and Firecrawl adapter unit tests.
- Validate Compose and confirm only the edge proxy publishes a public port.
- From retrieval containers, verify public HTTPS works while platform databases, tenant services, the Docker socket, host gateway, private CIDRs, and metadata endpoints are unreachable.
- Exercise `mainspring_search_web` and `mainspring_read_web_page`, then verify returned citation IDs and tenant audit events.
