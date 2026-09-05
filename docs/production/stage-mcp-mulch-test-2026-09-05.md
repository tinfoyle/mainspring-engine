# Stage MCP mulch-report test — 2026-09-05

Result: blocked at client authorization. No research, schedule, Agent run or
email delivery was performed.

## Live requests

- Protected-resource discovery at the Stage MCP origin returned HTTP 200.
  It identifies the Stage app as the authorization server and requires the
  spyglass:mcp scope with a Bearer header.
- Authorization-server discovery at the Stage app returned HTTP 200.
  It advertises S256 PKCE, authorization-code and refresh-token grants and
  HTTPS Client ID Metadata Document support. It has no dynamic registration
  endpoint.
- A real MCP tools/list POST, with modern protocol and mirror headers and
  a synthetic Account UUID, returned HTTP 401 and the correct Bearer discovery
  challenge. This verifies unauthenticated denial, not customer tool access.
- A Codex MCP login attempt using temporary command-line configuration and
  scope spyglass:mcp exited with:
  "Registration failed: Dynamic registration failed: Registration failed:
  Dynamic client registration not supported".

No Spyglass MCP server or OAuth client identity is configured in the current
desktop task or UbuntuRojo Codex configuration. The probe did not persist an
MCP server definition or issue a token.

## Code findings

The current MCP registry exposes scoped web research and approved Marketing
delivery preparation. It does not expose schedule creation or direct Agent-run
start operations. The API/MCP interaction guide explicitly retains those
workspace operations on HTTP and internal runner/schedule boundaries.

Therefore successful OAuth setup alone would permit testing available research
and delivery tools, but would not enable the full recurring report through MCP.

## Required follow-up

Configure a valid hosted HTTPS client metadata document and an exact approved
redirect URI for the test client, then complete the normal user-consent and
PKCE exchange. Alternatively, deliberately implement and certify another
supported client-registration path. Do not issue tokens through database
edits or weaken authentication.

After connection, discover the actual tools and authorized Account connections,
test research against known prices, and separately test approved delivery to a
designated test inbox. A complete MCP-only journey also needs supported
schedule/run tools and the report-to-email connection described in the prior
workflow review.
