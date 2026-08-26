package browserapp

const pageTemplates = `
{{define "head"}}
<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · Infinite Ocean: Spyglass</title><meta name="description" content="Infinite Ocean: Spyglass business operating system">
<link rel="stylesheet" href="/assets/spyglass.css?v=3">{{if .Script}}<script src="{{.Script}}?v=3" defer></script>{{end}}{{if .PrivacyControls}}<script src="/assets/privacy-analytics.js?v=3" defer></script>{{end}}</head><body><a class="skip-link" href="#main-content">Skip to main content</a>{{if .PrivacyControls}}{{template "privacy-controls" .}}{{end}}
{{end}}

{{define "privacy-controls"}}
<section id="privacy-consent" class="privacy-consent" aria-labelledby="privacy-title" hidden><p class="eyebrow">YOUR CHOICE</p><h2 id="privacy-title">Optional analytics</h2><p>Necessary storage keeps signup and security working. Content-free first-party analytics helps improve onboarding. It is optional and never changes access, checkout, or Affiliate attribution.</p><div id="privacy-options" class="privacy-options" hidden><label><span><strong>Analytics</strong><small>Content-free journey events</small></span><input id="privacy-analytics" type="checkbox"></label><label><span><strong>Marketing</strong><small>Unused at launch</small></span><input id="privacy-marketing" type="checkbox"></label><button type="button" data-privacy-save>Save preferences</button></div><div class="privacy-actions"><button type="button" data-privacy-accept>Accept analytics</button><button class="secondary" type="button" data-privacy-reject>Reject non-essential</button><button class="secondary" type="button" data-privacy-manage>Manage preferences</button></div><p id="privacy-error" class="privacy-error" role="alert"></p></section><button id="privacy-reopen" class="privacy-reopen" type="button" hidden>Privacy choices</button>{{if .AnalyticsEvent}}<span id="analytics-marker" hidden data-event="{{.AnalyticsEvent}}" data-event-id="{{.AnalyticsEventID}}" data-occurred-at="{{.AnalyticsOccurredAt}}"{{if .AnalyticsEventSecond}} data-event-second="{{.AnalyticsEventSecond}}" data-event-second-id="{{.AnalyticsEventSecondID}}"{{end}}{{if .OfferCode}} data-offer="{{.OfferCode}}"{{end}}{{if .AnalyticsMethod}} data-method="{{.AnalyticsMethod}}"{{end}}></span>{{end}}
{{end}}

{{define "brand"}}
<a class="brand" href="https://infiniteocean.net" aria-label="Infinite Ocean home"><i aria-hidden="true"><b></b></i><span><strong>INFINITE OCEAN</strong><small>SPYGLASS</small></span></a>
{{end}}

{{define "alert"}}
{{if .Error}}<div class="alert error" role="alert">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="alert success" role="status">{{.Notice}}</div>{{end}}
{{end}}

{{define "private-sidebar"}}
  <aside class="sidebar" aria-label="Spyglass navigation">
    {{template "brand" .}}
    <form class="account-switch" method="post" action="/app/account">
      <label>ACTIVE ACCOUNT<select name="account_id">{{range .Choices}}<option value="{{.AccountID}}" {{if $.Selected}}{{if eq .AccountID $.Selected.AccountID}}selected{{end}}{{end}}>{{.DisplayName}}</option>{{end}}</select></label>
      <button type="submit">Switch Account</button>
    </form>
    <nav aria-label="Primary navigation"><p>OPERATE</p><a {{if eq .Page "app"}}class="active" aria-current="page"{{end}} href="/app"><i aria-hidden="true">⌂</i>Overview</a><a {{if eq .Page "your-turn"}}class="active" aria-current="page"{{end}} href="/app/your-turn"><i aria-hidden="true">!</i>Your Turn</a><a {{if eq .Page "work"}}class="active" aria-current="page"{{end}} href="/app/work"><i aria-hidden="true">✓</i>Work</a><a {{if eq .Page "agents"}}class="active" aria-current="page"{{end}} href="/app/agents"><i aria-hidden="true">◌</i>Agents</a><a {{if eq .Page "schedules"}}class="active" aria-current="page"{{end}} href="/app/schedules"><i aria-hidden="true">◷</i>Schedules</a><a {{if eq .Page "knowledge"}}class="active" aria-current="page"{{end}} href="/app/knowledge"><i aria-hidden="true">◇</i>Knowledge</a><p>BUSINESS</p><a {{if eq .Page "finance"}}class="active" aria-current="page"{{end}} href="/app/finance"><i aria-hidden="true">≋</i>Finance</a><a {{if eq .Page "marketing"}}class="active" aria-current="page"{{end}} href="/app/marketing"><i aria-hidden="true">↗</i>Marketing</a><a {{if eq .Page "integrations"}}class="active" aria-current="page"{{end}} href="/app/integrations"><i aria-hidden="true">⌁</i>Integrations</a><a href="/app#billing"><i aria-hidden="true">$</i>Billing</a><a href="/app#settings"><i aria-hidden="true">⚙</i>Account</a><a {{if eq .Page "account-exports"}}class="active" aria-current="page"{{end}} href="/app/account-exports"><i aria-hidden="true">⇩</i>Exports</a><a {{if eq .Page "closures"}}class="active" aria-current="page"{{end}} href="/app/account-closures"><i aria-hidden="true">○</i>Lifecycle</a><a href="/app/security"><i aria-hidden="true">◇</i>Security</a></nav>
    <form method="post" action="/logout"><button class="logout" type="submit">Sign out</button></form>
  </aside>
{{end}}

{{define "private-topbar"}}
<header class="topbar"><div><strong>{{if .Selected}}{{.Selected.DisplayName}}{{else}}No Account selected{{end}}</strong><small>Infinite Ocean: Spyglass</small></div><span class="live"><i aria-hidden="true"></i>Account services ready</span></header>
{{end}}

{{define "login"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">WELCOME BACK</p><h1>Find the signal.<br><em>Move the business.</em></h1><p>Sign in once, then choose the Spyglass Account where you want to work.</p></div><footer>One identity · Explicit Account access · Package-aware</footer></section><section class="auth-panel"><form method="post" action="/login"><p class="eyebrow">SECURE ACCESS</p><h2>Sign in to Spyglass</h2>{{template "alert" .}}<input type="hidden" name="return_to" value="{{.ReturnTo}}"><label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><label>Password<input type="password" name="password" autocomplete="current-password" required></label><p class="form-note"><a href="/forgot-password{{if .ReturnTo}}?return_to={{urlquery .ReturnTo}}{{end}}">Forgot your password?</a></p><button type="submit">Sign in <span>→</span></button>{{if .PasskeysConfigured}}<div class="auth-divider"><span>or</span></div><button class="passkey-button secondary" id="passkey-login" type="button" data-return-to="{{.ReturnTo}}">Sign in with a passkey <span>◇</span></button><p class="passkey-status" id="passkey-status" role="status"></p>{{end}}<p class="form-note">New to Infinite Ocean? <a href="/signup{{if .ReturnTo}}?return_to={{urlquery .ReturnTo}}{{end}}">Create a free Account</a>.</p></form></section></main></body></html>
{{end}}

{{define "forgot"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY RECOVERY</p><h1>Restore access.<br><em>Keep every Account.</em></h1><p>Your Infinite Ocean identity spans Spyglass Accounts. Recover the identity once; no Account data or Membership is recreated.</p></div><footer>Single-use link · 30-minute expiry · Every session revoked</footer></section><section class="auth-panel"><form method="post" action="/forgot-password"><p class="eyebrow">RECOVERY LINK</p><h2>Find your identity</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development recovery</strong><a href="/reset-password?token={{.DevelopmentToken}}{{if .ReturnTo}}&return_to={{urlquery .ReturnTo}}{{end}}">Set a new password</a></div>{{else}}<label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label>{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}<button type="submit">Send recovery link <span>→</span></button>{{end}}<p class="form-note"><a href="/login{{if .ReturnTo}}?return_to={{urlquery .ReturnTo}}{{end}}">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "reset"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">SECURE THE IDENTITY</p><h1>Set a new key<br><em>to the view ahead.</em></h1><p>Completing recovery invalidates the link, changes the password, and signs the identity out everywhere.</p></div><footer>Argon2id credential · Security version advanced · Sessions revoked</footer></section><section class="auth-panel"><form method="post" action="/reset-password"><p class="eyebrow">NEW PASSWORD</p><h2>Reset your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}">{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}<label>New password<input type="password" name="password" autocomplete="new-password" minlength="12" aria-describedby="password-requirements" required><small id="password-requirements">Use at least 12 characters.</small></label><button type="submit">Update password <span>→</span></button><p class="form-note"><a href="/login{{if .ReturnTo}}?return_to={{urlquery .ReturnTo}}{{end}}">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "signup"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">START WITH CLARITY</p><h1>Your first clear<br><em>view is free.</em></h1><p>Create a system-wide identity and a real Spyglass Account. Payment information is optional.</p></div><footer>Free plan · No customer container · Upgrade by package</footer></section><section class="auth-panel"><form method="post" action="/signup"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h2>Create your Account</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development verification</strong><a href="/verify?token={{.DevelopmentToken}}{{if .OfferCode}}&offer={{.OfferCode}}{{end}}{{if .ReturnTo}}&return_to={{urlquery .ReturnTo}}{{end}}">Continue to password setup</a></div>{{else}}<label>Your name<input name="name" value="{{.Name}}" autocomplete="name" required></label><label>Work email<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><label>Business name<input name="account_name" value="{{.AccountName}}" autocomplete="organization" required></label><input type="hidden" name="region" value="us-east">{{if .OfferCode}}<input type="hidden" name="offer_code" value="{{.OfferCode}}">{{end}}{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}<button type="submit">Continue securely <span>→</span></button>{{end}}<p class="form-note">Already registered? <a href="/login{{if .ReturnTo}}?return_to={{urlquery .ReturnTo}}{{end}}">Sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "verify"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY VERIFIED</p><h1>Secure the<br><em>view ahead.</em></h1><p>Your password protects your Infinite Ocean identity across every Account you are invited to join.</p></div><footer>12+ characters · Rotating sessions · Account isolation</footer></section><section class="auth-panel"><form method="post" action="/verify"><p class="eyebrow">FINAL STEP</p><h2>Choose your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}">{{if .OfferCode}}<input type="hidden" name="offer_code" value="{{.OfferCode}}">{{end}}{{if .ReturnTo}}<input type="hidden" name="return_to" value="{{.ReturnTo}}">{{end}}<label>Password<input type="password" name="password" autocomplete="new-password" minlength="12" aria-describedby="password-requirements" required><small id="password-requirements">Use at least 12 characters.</small></label><button type="submit">Create identity and Account <span>→</span></button></form></section></main></body></html>
{{end}}

{{define "contact-verify"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">VERIFIED CONTACT</p><h1>Move the signal.<br><em>Keep the identity.</em></h1><p>Confirming the new mailbox changes the login for every Spyglass Account and closes every existing session.</p></div><footer>Single-use link · System-wide identity · Sessions revoked</footer></section><section class="auth-panel"><form method="post" action="/contact-change/verify"><p class="eyebrow">EMAIL CONFIRMATION</p><h2>Verify the new email</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><p class="form-note">This changes only your Infinite Ocean identity email. Account Memberships, roles, and package access stay intact.</p><button type="submit" {{if not .Token}}disabled{{end}}>Change identity email <span>→</span></button><p class="form-note"><a href="/login">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "accept"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">YOU'RE INVITED</p><h1>Bring another<br><em>Account into view.</em></h1><p>The Membership will be added to your existing Infinite Ocean identity.</p></div></section><section class="auth-panel"><form method="post" action="/invitations/accept"><p class="eyebrow">ACCOUNT MEMBERSHIP</p><h2>Accept invitation</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><button type="submit">Join this Account <span>→</span></button><p class="form-note"><a href="/app">Return to Spyglass</a></p></form></section></main></body></html>
{{end}}

{{define "security"}}
{{template "head" .}}
<main class="security-layout" id="main-content" tabindex="-1">
  <header>{{template "brand" .}}{{if .ReturnTo}}<a href="{{.ReturnTo}}">Return to previous page</a>{{else}}<a href="/app">Return to Spyglass</a>{{end}}</header>
  <section class="security-hero"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h1>Security follows<br><em>you, not an Account.</em></h1><p>Review every active Spyglass session, manage passkeys, or confirm your identity before a sensitive change.</p></section>
  <div class="security-grid">
    <section class="security-card"><p class="eyebrow">IDENTITY CONFIRMATION</p><h2>Unlock sensitive actions</h2>{{template "alert" .}}<p>Password confirmation unlocks factor recovery. A user-verified passkey unlocks verified-email changes and privileged Account actions—including Membership, invitation, and billing changes—for 10 minutes on this session.</p><form method="post" action="/app/security/reauthenticate"><input type="hidden" name="return_to" value="{{.ReturnTo}}"><label>Current password<input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Confirm password</button></form>{{if .PasskeysConfigured}}{{if .Passkeys}}<div class="security-or"><span>or</span></div><button class="passkey-button secondary" id="passkey-reauthenticate" type="button" data-return-to="{{.ReturnTo}}">Unlock with a passkey</button>{{end}}<p class="passkey-status" id="passkey-status" role="status"></p>{{end}}</section>
    {{if .ContactChangesConfigured}}<section class="security-card identity-contact"><p class="eyebrow">VERIFIED CONTACT</p><h2>Identity email</h2><p>Your current login is <strong>{{.CurrentEmail}}</strong>. Changing it requires a recent passkey confirmation and verification from the new mailbox. Every session is revoked when the change completes.</p><form method="post" action="/app/security/contact-change"><label>New email<input type="email" name="new_email" value="{{.NewEmail}}" autocomplete="email" required></label><button type="submit">Send verification</button></form>{{if .DevelopmentToken}}<div class="dev-link"><strong>Development email verification</strong><a href="/contact-change/verify?token={{.DevelopmentToken}}">Verify the new email</a></div>{{end}}</section>{{end}}
    <section class="security-card"><div class="security-card-head"><div><p class="eyebrow">ACTIVE SESSIONS</p><h2>Where you are signed in</h2></div><form method="post" action="/app/security/sessions/revoke-all"><button class="danger" type="submit">Sign out everywhere</button></form></div><div class="session-list">{{range .ActiveSessions}}<article><div><strong>{{.ClientLabel}}</strong>{{if .Current}}<em>Current session</em>{{end}}<small>Signed in with {{.AuthenticationMethod}} · Last confirmed with {{.ReauthenticationMethod}}</small><small>Last used {{.LastSeenAt.Format "Jan 2, 2006 at 15:04 UTC"}} · Expires {{.ExpiresAt.Format "Jan 2, 2006"}}</small></div><form method="post" action="/app/security/sessions/revoke"><input type="hidden" name="session_id" value="{{.ID}}"><button type="submit">{{if .Current}}Sign out{{else}}Revoke{{end}}</button></form></article>{{else}}<p>No active sessions.</p>{{end}}</div></section>
    {{if .MCPGrantsConfigured}}<section class="security-card"><div class="security-card-head"><div><p class="eyebrow">CONNECTED MCP CLIENTS</p><h2>Applications acting as you</h2></div></div><p>Each connection can use Spyglass MCP with your current Account memberships and package access. Revoking a client immediately invalidates all access and refresh credentials issued by that connection.</p><div class="session-list">{{range .MCPGrants}}<article><div><strong>{{.ClientName}}</strong><small>{{.ClientID}}</small><small>Connected {{.CreatedAt.Format "Jan 2, 2006 at 15:04 UTC"}}{{if .LastUsedAt}} · Last used {{.LastUsedAt.Format "Jan 2, 2006 at 15:04 UTC"}}{{end}}</small></div><form method="post" action="/app/security/mcp-grants/revoke"><input type="hidden" name="grant_id" value="{{.ID}}"><button class="danger" type="submit">Revoke</button></form></article>{{else}}<p>No MCP clients are connected.</p>{{end}}</div></section>{{end}}
    <section class="security-card factor-loss-policy"><p class="eyebrow">FACTOR-LOSS POLICY</p><h2>Know the last-resort path</h2><ol><li><b>1</b><span><strong>Recover the password if needed</strong><small>The email recovery link changes the password and signs every session out.</small></span></li><li><b>2</b><span><strong>Use one saved recovery code</strong><small>It unlocks replacement-passkey enrollment only for this User and session, for 10 minutes.</small></span></li><li><b>3</b><span><strong>Add a passkey and replace the code set</strong><small>Owner authority stays closed until a passkey and at least one unused code both exist.</small></span></li></ol><p class="factor-loss-warning"><strong>Infinite Ocean support cannot view or recreate recovery codes, impersonate a passkey, or mark an owner ready.</strong> If every authenticator and every saved code are unavailable, self-service owner recovery is not possible and Account authority remains locked.</p></section>
    {{if .PasskeysConfigured}}<section class="security-card security-passkeys"><div class="security-card-head"><div><p class="eyebrow">PASSKEYS</p><h2>Phishing-resistant sign-in</h2></div></div><p>Passkeys belong to your Infinite Ocean identity and work across every Spyglass Account you can access. Confirm with an existing passkey before adding another. If every passkey is lost, confirm your password and use one saved recovery code.</p><div class="passkey-list">{{range .Passkeys}}<article><div><strong>{{.Name}}</strong><small>Added {{.CreatedAt.Format "Jan 2, 2006"}}{{if .LastUsedAt}} · Last used {{.LastUsedAt.Format "Jan 2, 2006"}}{{end}}{{if .BackedUp}} · Synced{{end}}</small></div><div><button class="passkey-rename secondary" type="button" data-credential-id="{{.ID}}" data-credential-name="{{.Name}}">Rename</button><button class="passkey-remove" type="button" data-credential-id="{{.ID}}">Remove</button><button class="passkey-compromise secondary" type="button" data-credential-id="{{.ID}}">Report compromised</button></div></article>{{else}}<p>No passkeys have been added.</p>{{end}}</div><div class="passkey-enroll"><label>Passkey name<input id="passkey-name" maxlength="80" value="My passkey" autocomplete="off"></label><button id="passkey-register" type="button" data-return-to="{{.ReturnTo}}">Add passkey</button></div></section>{{end}}
    {{if .RecoveryCodesConfigured}}<section class="security-card"><div class="security-card-head"><div><p class="eyebrow">FACTOR RECOVERY</p><h2>One-time recovery codes</h2></div></div>{{if .RecoveryCodeStatus.Configured}}<p><strong>{{.RecoveryCodeStatus.Remaining}} of 10</strong> codes remain from set {{.RecoveryCodeStatus.Version}}. Creating a new set immediately revokes every previous code.</p>{{else}}<p>Create recovery codes after enrolling your first passkey. They are the only self-service path for replacing every lost passkey.</p>{{end}}{{if or .Passkeys .RecoveryCodeStatus.Configured}}<form method="post" action="/app/security/recovery-codes"><button type="submit">{{if .RecoveryCodeStatus.Configured}}Replace recovery codes{{else}}Create recovery codes{{end}}</button></form>{{else}}<p><strong>Add a passkey first.</strong> Recovery-code sets can only be created after passkey verification.</p>{{end}}{{if .RecoveryCodeStatus.Configured}}<details class="lost-passkey"><summary>Lost access to every passkey?</summary><p>Confirm your password above, then spend one saved code to unlock a replacement passkey on this session. This works even while the lost credentials still appear in your passkey list.</p><form method="post" action="/app/security/recovery-codes/consume"><label>Saved recovery code<input name="code" autocomplete="one-time-code" required></label><button class="secondary" type="submit">Unlock replacement passkey</button></form></details>{{end}}{{if .RecoveryCodes}}<div class="recovery-code-list" role="list" aria-label="New recovery codes">{{range .RecoveryCodes}}<code role="listitem">{{.}}</code>{{end}}</div><p><strong>Save these now.</strong> Spyglass stores only one-way hashes and cannot display this set again.</p>{{end}}</section>{{end}}
    <section class="security-card security-events"><p class="eyebrow">SECURITY HISTORY</p><h2>Recent identity activity</h2><div class="event-list">{{range .SecurityEvents}}<article><i aria-hidden="true"></i><div><strong>{{.Label}}</strong><small>{{.Detail}}</small></div><time>{{.OccurredAt.Format "Jan 2, 2006 at 15:04 UTC"}}</time></article>{{else}}<p>No security events have been recorded.</p>{{end}}</div></section>
  </div>
</main></body></html>
{{end}}

{{define "app"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content">
      {{template "alert" .}}
      {{if .DevelopmentToken}}<div class="dev-link"><strong>Development invitation link</strong><a href="/invitations/accept?token={{.DevelopmentToken}}">Open invitation</a></div>{{end}}
      {{if .Selected}}
      {{if .OwnerEnrollmentRequired}}<section class="owner-security-gate"><div><p class="eyebrow">OWNER IDENTITY SETUP</p><h1>Secure the helm<br><em>before taking command.</em></h1><p>Ownership carries Membership, billing, and lifecycle authority. Add a phishing-resistant passkey, then save a set of one-time recovery codes. Until both are ready, Spyglass keeps this Account in preview and rejects every Account authorization boundary.</p></div><ol><li><b>01</b><span><strong>Add a passkey</strong><small>Proves user presence and verification for privileged actions.</small></span></li><li><b>02</b><span><strong>Save recovery codes</strong><small>Preserves a governed path if every passkey is lost.</small></span></li></ol><a class="button" href="/app/security">Secure owner identity</a></section>{{end}}
      <section class="hero-panel"><div><p class="eyebrow">ACCOUNT OVERVIEW</p>{{if .OwnerEnrollmentRequired}}<h2 class="account-state-heading">Your Account exists.<br><em>Owner setup comes next.</em></h2><p>Placement and package previews are available, but Account data and operations remain closed until owner identity setup is complete.</p>{{else}}<h1>Your Account is ready.<br><em>The operating surface comes next.</em></h1><p>Identity, Membership, placement, package access, and the local entitlement snapshot are active for this Account.</p>{{end}}</div><div class="horizon" aria-hidden="true"><i></i><b></b></div></section>
      <section class="metrics">
        <article><small>ACCOUNT TYPE</small><strong>{{.Selected.AccountType}}</strong><span class="green">{{.BillingState}}</span></article>
        <article><small>PACKAGE ACCESS</small><strong>{{len .PackageModes}}</strong><span>Effective packages</span></article>
        <article><small>YOUR ROLE</small><strong>{{.Selected.Role}}</strong><span>Active Membership</span></article>
        <article><small>ACCESS VERSION</small><strong>{{.Selected.Entitlements.Version}}</strong><span>Local snapshot</span></article>
      </section>
      <div class="dashboard-grid">
        <section class="panel" id="work"><header><div><p class="eyebrow">WORK</p><h2>What’s moving</h2></div><span>Module boundary reserved</span></header><div class="work-empty"><strong>No operational work has been created.</strong><p>The production Work module will populate this view through Account-scoped queries; this shell does not invent customer activity.</p></div></section>
        <aside class="mia"><header><b>M</b><div><small>YOUR OPERATING PARTNER</small><strong>Mia</strong></div><i></i></header><p>No items are waiting for your attention. Mia will work only through enabled packages and explicitly granted capabilities.</p><button type="button" disabled>Nothing waiting</button><footer><i></i>Bound to this Account’s permissions</footer></aside>
      </div>
      <section class="packages"><header><div><p class="eyebrow">FEATURE PACKAGES</p><h2>Your operating surface</h2></div><span>{{.Selected.AccountType}} Account</span></header><div>{{range .Catalog.Packages}}<article id="{{.Code}}"><b>·</b><div><strong>{{.Name}}</strong><small>{{.Description}}</small></div><em>{{with index $.PackageModes .Code}}{{.}}{{else}}locked{{end}}</em></article>{{end}}</div></section>
      <section class="billing panel" id="billing">
        <header><div><p class="eyebrow">BILLING & ACCESS</p><h2>Choose the operating surface that earns its place</h2></div><span>{{.BillingSynced}}</span></header>
        <div class="billing-summary"><div><small>LOCAL BILLING STATE</small><strong>{{.BillingState}}</strong><span>{{.BillingPeriod}}</span></div>{{if and .CanManageBilling .HasBillingCustomer}}<form method="post" action="/app/billing/portal"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><button type="submit">Manage billing ↗</button></form>{{end}}</div>
        <div class="plan-grid">{{range .BillingPlans}}<article class="plan-card {{if .Current}}current{{end}} {{if .Selected}}selected{{end}}"><div><small>{{if .Current}}CURRENT PLAN{{else if .Selected}}SELECTED ON INFINITE OCEAN{{else}}{{.PackageCount}} PACKAGES{{end}}</small><h3>{{.Name}}</h3><p>{{.Description}}</p></div><div class="plan-price"><strong>{{.Price}}</strong><span>/ {{.Interval}}</span></div>{{if .Current}}<button type="button" disabled>Current access</button>{{else if not $.BillingConfigured}}<span class="plan-note">Paid billing is disabled in this development environment.</span>{{else if $.CanStartCheckout}}<form method="post" action="/app/billing/checkout"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="offer_code" value="{{.OfferCode}}"><button type="submit">Choose {{.Name}} →</button></form>{{else if $.CanManageBilling}}{{if $.HasBillingCustomer}}<span class="plan-note">Use the billing portal to change plans.</span>{{else}}<span class="plan-note">Checkout is not available yet.</span>{{end}}{{else}}<span class="plan-note">An Account billing administrator can manage this plan.</span>{{end}}</article>{{end}}</div>
        {{if not .BillingConfigured}}<p class="billing-footnote">Paid billing is intentionally unavailable in this development-only memory environment. The persistent Spyglass service enables these controls.</p>{{else}}<p class="billing-footnote">Checkout returns here in a processing state. Package access changes only after a signed Stripe event is projected into the local entitlement snapshot.</p>{{end}}
      </section>
      {{if .CanManageMembers}}<section class="team panel" id="settings">
        <header><div><p class="eyebrow">ACCOUNT MEMBERSHIP</p><h2>People with access</h2></div><span>Your role: {{.Selected.Role}}</span></header>
        <div class="member-list">{{range .Members}}<article>
          <div class="member-identity"><strong>{{.DisplayName}}{{if .IsSelf}} <em>You</em>{{end}}</strong><small>{{.Email}}</small><span>{{.Role}} · {{.State}} · version {{.Version}}</span></div>
          {{if .CanChangeRole}}<form method="post" action="/app/memberships/role"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="membership_id" value="{{.MembershipID}}"><input type="hidden" name="version" value="{{.Version}}"><label>Role<select name="role"><option value="administrator" {{if eq .Role "administrator"}}selected{{end}}>Administrator</option><option value="billing_admin" {{if eq .Role "billing_admin"}}selected{{end}}>Billing admin</option><option value="member" {{if eq .Role "member"}}selected{{end}}>Member</option><option value="viewer" {{if eq .Role "viewer"}}selected{{end}}>Viewer</option></select></label><label>Audit reason<input name="reason" minlength="3" maxlength="300" value="Role responsibility updated" required></label><button type="submit">Update role</button></form>{{end}}
          {{if .CanTransfer}}<form class="ownership-form" method="post" action="/app/ownership-transfer"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="membership_id" value="{{.MembershipID}}"><input type="hidden" name="actor_version" value="{{$.ActorMembershipVersion}}"><input type="hidden" name="version" value="{{.Version}}"><input type="hidden" name="reason" value="Account ownership transferred"><label>Type TRANSFER<input name="confirmation" pattern="TRANSFER" autocomplete="off" required></label><button type="submit">Transfer ownership</button></form>{{end}}
          {{if .CanRemove}}<form class="remove-member-form" method="post" action="/app/memberships/remove"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="membership_id" value="{{.MembershipID}}"><input type="hidden" name="version" value="{{.Version}}"><input type="hidden" name="reason" value="Account access no longer required"><button type="submit">Remove access</button></form>{{end}}
          {{if .CanSuspend}}<form class="state-member-form" method="post" action="/app/memberships/suspend"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="membership_id" value="{{.MembershipID}}"><input type="hidden" name="version" value="{{.Version}}"><input type="hidden" name="reason" value="Account access temporarily suspended"><button type="submit">Suspend</button></form>{{end}}
          {{if .CanReactivate}}<form class="state-member-form" method="post" action="/app/memberships/reactivate"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="membership_id" value="{{.MembershipID}}"><input type="hidden" name="version" value="{{.Version}}"><input type="hidden" name="reason" value="Account access restored"><button type="submit">Reactivate</button></form>{{end}}
        </article>{{else}}<p>No active Memberships were found.</p>{{end}}</div>
        {{if .CanInvite}}<div class="invite-member"><div><p class="eyebrow">INVITE</p><h3>Bring a teammate into view</h3><p>The invitation joins an existing Infinite Ocean identity to this Account.</p></div><form method="post" action="/app/invitations"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><label>Email<input type="email" name="email" required placeholder="teammate@company.com"></label><label>Role<select name="role"><option value="member">Member</option><option value="viewer">Viewer</option><option value="administrator">Administrator</option><option value="billing_admin">Billing admin</option></select></label><button type="submit">Send invitation</button></form></div>{{end}}
        <p class="team-footnote">Role, lifecycle, removal, and ownership changes require a passkey confirmation from the acting identity. Ownership is transferred atomically and every mutation records an immutable reason.</p>
      </section>{{end}}
      {{if .CanCloseAccount}}<section class="account-close panel"><header><div><p class="eyebrow">ACCOUNT LIFECYCLE</p><h2>Close this Account</h2></div><span>7-day cooling-off period</span></header><div class="account-close-body"><div><p>Requesting closure immediately freezes this Account for every member. The Account remains recoverable from the global Lifecycle page during cooling-off.</p><p>Active Stripe subscriptions and checkout sessions must be resolved first. Logical closure retains governed records until the separate retention deadline; it does not synchronously erase data.</p></div><form method="post" action="/app/account-closures/request"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><input type="hidden" name="account_version" value="{{.Selected.AccountVersion}}"><label>Audit reason<input name="reason" minlength="3" maxlength="300" value="Account is no longer required" required></label><label>Type CLOSE<input name="confirmation" pattern="CLOSE" autocomplete="off" required></label><button type="submit">Request Account closure</button></form></div></section>{{end}}
      {{if .CanLeaveAccount}}<section class="account-leave panel"><header><div><p class="eyebrow">YOUR ACCOUNT ACCESS</p><h2>Leave this Account</h2></div><span>Other Accounts are unaffected</span></header><form method="post" action="/app/memberships/leave"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><input type="hidden" name="version" value="{{.ActorMembershipVersion}}"><input type="hidden" name="reason" value="Member chose to leave the Account"><label>Type LEAVE<input name="confirmation" pattern="LEAVE" autocomplete="off" required></label><button type="submit">Leave Account</button></form><p>Leaving is permanent for this Membership. An owner or administrator must invite you again to restore access.</p></section>{{end}}
      {{else}}<section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>{{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "work"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content work-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .WorkAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">WORK PACKAGE</p><h1>Bring every commitment<br><em>into one clear view.</em></h1><p>Work is not included in this Account's current package set. Upgrade to Team or Operating to coordinate accountable work across people and agents.</p><a href="/app#billing">Review Account plans →</a></div><div class="work-locked-map" aria-hidden="true"><i></i><i></i><i></i><b></b></div></section>
      {{else}}
      <section class="work-heading"><div><p class="eyebrow">WORK</p><h1>What is moving,<br><em>what needs attention.</em></h1><p>One Account-scoped queue for human commitments, agent activity, and operational follow-through.</p></div><div class="work-heading-actions"><span class="work-mode">{{if .WorkReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span>{{if not .WorkReadOnly}}<button class="work-new" id="work-create-open" type="button">＋ New work</button>{{end}}</div></section>
      <section class="work-app" id="work-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.WorkReadOnly}}" aria-busy="true">
        <p class="sr-only" id="work-command-status" role="status" aria-live="polite" aria-atomic="true"></p>
        <div class="work-summary" role="region" aria-label="Work summary">
          <article><small>ACTIVE</small><strong data-summary="active">—</strong><span>Open commitments</span></article>
          <article><small>IN PROGRESS</small><strong data-summary="in_progress">—</strong><span>Moving now</span></article>
          <article><small>WAITING</small><strong data-summary="waiting">—</strong><span>Needs a signal</span></article>
          <article><small>URGENT</small><strong data-summary="urgent">—</strong><span>Highest priority</span></article>
          <article><small>DONE</small><strong data-summary="done">—</strong><span>Recently complete</span></article>
        </div>
        <div class="work-layout">
          <section class="work-queue panel">
            <header><div><p class="eyebrow">ACCOUNT QUEUE</p><h2>Operational work</h2></div><span id="work-result-count">Loading</span></header>
            <form class="work-filters" id="work-filters">
              <label><span class="sr-only">Search work</span><input type="search" name="q" maxlength="200" placeholder="Search title or description"></label>
              <label><span class="sr-only">Filter by state</span><select name="state"><option value="">All states</option><option value="open">Open</option><option value="in_progress">In progress</option><option value="waiting">Waiting</option><option value="done">Done</option><option value="canceled">Canceled</option></select></label>
              <label><span class="sr-only">Filter by kind</span><select name="kind"><option value="">All kinds</option><option value="todo">To-do</option><option value="ticket">Ticket</option></select></label>
              <button type="submit">Apply</button>
            </form>
            <div class="work-status" id="work-status" role="status">Loading Account work…</div>
            <div class="work-list" id="work-list" role="region" aria-label="Work items"></div>
            <button class="work-more" id="work-more" type="button" hidden>Load more</button>
          </section>
          <aside class="work-detail panel" id="work-detail" tabindex="-1" aria-live="polite">
            <div class="work-detail-empty"><p class="eyebrow">WORK DETAIL</p><h2>Select an item</h2><p>Open a queue item to inspect its responsibility, origin, timing, and current state.</p></div>
          </aside>
        </div>
      </section>
      {{if not .WorkReadOnly}}
      <dialog class="work-dialog" id="work-create-dialog" aria-labelledby="work-create-title"><form id="work-create-form"><header><div><p class="eyebrow">NEW WORK</p><h2 id="work-create-title">Create a clear next step</h2></div><button id="work-create-close" type="button" aria-label="Close">×</button></header><label>Title<input name="title" maxlength="240" required placeholder="What needs to happen?" aria-describedby="work-draft-note"></label><label>Description<textarea name="description" maxlength="20000" rows="5" placeholder="Add the outcome, context, and definition of done."></textarea></label><div class="work-form-grid"><label>Type<select name="kind"><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><label>Priority<select name="priority"><option value="normal">Normal</option><option value="high">High</option><option value="urgent">Urgent</option><option value="low">Low</option></select></label><label>Responsibility<select name="responsibility" id="work-create-responsibility"><option value="shared">Shared</option><option value="user">Assign to me</option><option value="external">External owner</option><option value="persona" disabled>Agent (no active Persona)</option></select></label></div><label id="work-create-external-field" hidden>External owner reference<input name="external_ref" minlength="2" maxlength="200" disabled placeholder="Team, vendor, or contact reference"></label><label id="work-create-persona-field" hidden>Agent Persona<select name="persona_id" id="work-create-persona" disabled></select></label><p class="work-draft-note" id="work-draft-note">This draft stays in this browser tab while you navigate Spyglass.</p><p class="work-form-error" id="work-create-error" role="alert" hidden></p><footer><button class="secondary" id="work-create-cancel" type="button">Keep draft and close</button><button class="primary" type="submit">Create work</button></footer></form></dialog>
      <dialog class="work-dialog work-command-dialog" id="work-transition-dialog" aria-labelledby="work-transition-title"><form id="work-transition-form"><header><div><p class="eyebrow">WORK COMMAND</p><h2 id="work-transition-title">Update work state</h2></div><button id="work-transition-close" type="button" aria-label="Close">×</button></header><p class="work-dialog-context" id="work-transition-context"></p><label>Operational reason<textarea name="reason" minlength="3" maxlength="1000" rows="4" required placeholder="Why is this state change appropriate now?"></textarea></label><p class="work-form-error" id="work-transition-error" role="alert" hidden></p><footer><button class="secondary" id="work-transition-cancel" type="button">Cancel</button><button class="primary" type="submit">Apply state change</button></footer></form></dialog>
      <dialog class="work-dialog work-command-dialog" id="work-assignment-dialog" aria-labelledby="work-assignment-title"><form id="work-assignment-form"><header><div><p class="eyebrow">WORK ASSIGNMENT</p><h2 id="work-assignment-title">Set responsibility</h2></div><button id="work-assignment-close" type="button" aria-label="Close">×</button></header><label>Responsibility<select name="responsibility" id="work-assignment-responsibility"><option value="shared">Shared</option><option value="user">Assign to me</option><option value="external">External owner</option><option value="persona" disabled>Agent (no active Persona)</option></select></label><label id="work-assignment-external-field" hidden>External owner reference<input name="external_ref" minlength="2" maxlength="200" disabled placeholder="Team, vendor, or contact reference"></label><label id="work-assignment-persona-field" hidden>Agent Persona<select name="persona_id" id="work-assignment-persona" disabled></select></label><label>Assignment reason<textarea name="reason" minlength="3" maxlength="1000" rows="3" required placeholder="Why should responsibility change?"></textarea></label><p class="work-form-error" id="work-assignment-error" role="alert" hidden></p><footer><button class="secondary" id="work-assignment-cancel" type="button">Cancel</button><button class="primary" type="submit">Update assignment</button></footer></form></dialog>
      {{end}}
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "knowledge"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content knowledge-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .KnowledgeAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">KNOWLEDGE PACKAGE</p><h1>Make evidence useful.<br><em>Keep authority human.</em></h1><p>Knowledge is not included in this Account's current package set.</p><a href="/app#billing">Review Account plans →</a></div></section>
      {{else}}
      <section class="work-heading"><div><p class="eyebrow">KNOWLEDGE</p><h1>What the Account knows,<br><em>and why it trusts it.</em></h1><p>Review source-attributed claims and inspect the current accepted fact projection. Agent output remains a proposal until a person decides.</p></div><span class="work-mode">{{if .KnowledgeReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span></section>
      <section class="knowledge-app" id="knowledge-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.KnowledgeReadOnly}}" aria-busy="true">
        <p class="sr-only" id="knowledge-command-status" role="status" aria-live="polite"></p>
        <div class="knowledge-layout">
          <section class="panel knowledge-queue"><header><div><p class="eyebrow">REVIEW QUEUE</p><h2>Proposed claims</h2></div><button class="secondary" id="knowledge-refresh" type="button">Refresh</button></header><p id="knowledge-claims-status" role="status">Loading claims…</p><div id="knowledge-claims" class="knowledge-list"></div></section>
          <aside class="panel knowledge-detail" id="knowledge-detail" tabindex="-1"><p class="eyebrow">CLAIM DETAIL</p><h2>Select a claim</h2><p>Inspect its exact canonical value and citations before deciding.</p></aside>
        </div>
        <section class="panel knowledge-facts"><header><div><p class="eyebrow">ACCEPTED PROJECTION</p><h2>Current facts</h2></div><span id="knowledge-facts-count">Loading</span></header><div id="knowledge-facts" class="knowledge-list"></div></section>
      </section>
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "marketing"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content marketing-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .MarketingAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">MARKETING PACKAGE</p><h1>Prepare the message.<br><em>Govern the release.</em></h1><p>Marketing becomes available when this Account has the package. Drafts stay provider-neutral and no activation sends externally.</p><a href="/app#billing">Review Account plans →</a></div></section>
      {{else}}
      <section class="marketing-heading"><div><p class="eyebrow">MARKETING</p><h1>Campaign intent,<br><em>released by judgment.</em></h1><p>Freeze creative revisions and approve one exact release before any Integration can deliver it.</p></div><div class="marketing-toolbar"><span class="work-mode">{{if .MarketingReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span><button id="marketing-refresh" type="button">Refresh</button>{{if not .MarketingReadOnly}}<button id="marketing-new-campaign" type="button">New campaign</button>{{end}}</div></section>
      <section class="marketing-app" id="marketing-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.MarketingReadOnly}}" aria-busy="true">
        <p class="sr-only" id="marketing-command-status" role="status" aria-live="polite" aria-atomic="true"></p>
        <div class="marketing-layout">
          <section class="panel marketing-campaigns"><header><div><p class="eyebrow">CAMPAIGNS</p><h2>Intent and state</h2></div><label><span class="sr-only">Campaign state</span><select id="marketing-state"><option value="">All states</option><option value="draft">Draft</option><option value="active">Active</option><option value="paused">Paused</option><option value="completed">Completed</option><option value="archived">Archived</option></select></label></header><p id="marketing-campaign-status" class="marketing-status" role="status">Loading campaigns…</p><div id="marketing-campaign-list" class="marketing-list"></div></section>
          <section class="marketing-stage">
            <article class="panel marketing-detail" id="marketing-campaign-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">CAMPAIGN DETAIL</p><h2>Select a campaign</h2><p>Inspect its frozen channel intent, creative revisions and governed releases.</p></article>
            <div class="marketing-columns">
              <section class="panel marketing-collection"><header><div><p class="eyebrow">CREATIVE</p><h2>Asset revisions</h2></div>{{if not .MarketingReadOnly}}<button id="marketing-new-asset" type="button" disabled>Add revision</button>{{end}}</header><p id="marketing-asset-status" class="marketing-status">Select a campaign.</p><div id="marketing-asset-list" class="marketing-list"></div></section>
              <section class="panel marketing-collection"><header><div><p class="eyebrow">RELEASES</p><h2>Approval snapshots</h2></div>{{if not .MarketingReadOnly}}<button id="marketing-new-release" type="button" disabled>Build release</button>{{end}}</header><p id="marketing-release-status" class="marketing-status">Select a campaign.</p><div id="marketing-release-list" class="marketing-list"></div></section>
            </div>
          </section>
        </div>
      </section>
      {{if not .MarketingReadOnly}}
      <dialog class="marketing-dialog" id="marketing-campaign-dialog"><form id="marketing-campaign-form"><header><div><p class="eyebrow">CAMPAIGN</p><h2 id="marketing-campaign-form-title">New campaign</h2></div><button type="button" data-close-marketing aria-label="Close">×</button></header><label>Name<input name="name" maxlength="160" required></label><label>Objective<textarea name="objective" maxlength="4000" rows="3" required></textarea></label><label>Audience<textarea name="audience" maxlength="4000" rows="3" required></textarea></label><fieldset><legend>Channels</legend><label><input type="checkbox" name="channel" value="email"> Email</label><label><input type="checkbox" name="channel" value="web"> Web</label></fieldset><p class="marketing-error" role="alert" hidden></p><footer><button type="button" data-close-marketing>Cancel</button><button type="submit">Save campaign</button></footer></form></dialog>
      <dialog class="marketing-dialog" id="marketing-asset-dialog"><form id="marketing-asset-form" enctype="multipart/form-data"><header><div><p class="eyebrow">IMMUTABLE CREATIVE</p><h2>Add asset revision</h2></div><button type="button" data-close-marketing aria-label="Close">×</button></header><label>Asset ID<input name="asset_id" pattern="[0-9a-fA-F-]{36}" required></label><div class="marketing-form-grid"><label>Kind<select name="kind"><option value="copy">Copy</option><option value="image">Image</option><option value="document">Document</option></select></label><label>Media type<input name="media_type" placeholder="text/plain" maxlength="100" required></label></div><label>Title<input name="title" maxlength="240" required></label><label>Creative file<input type="file" name="file" required></label><label>Alternative text<textarea name="alternative_text" maxlength="1000" rows="2"></textarea></label><p class="marketing-error" role="alert" hidden></p><footer><button type="button" data-close-marketing>Cancel</button><button type="submit">Append revision</button></footer></form></dialog>
      <dialog class="marketing-dialog" id="marketing-release-dialog"><form id="marketing-release-form"><header><div><p class="eyebrow">RELEASE SNAPSHOT</p><h2>Build release</h2></div><button type="button" data-close-marketing aria-label="Close">×</button></header><label>Name<input name="name" maxlength="160" required></label><fieldset><legend>Channels</legend><label><input type="checkbox" name="channel" value="email"> Email</label><label><input type="checkbox" name="channel" value="web"> Web</label></fieldset><label>Asset revision IDs<textarea name="asset_revision_ids" rows="3" placeholder="UUIDs separated by commas" required></textarea></label><p class="marketing-error" role="alert" hidden></p><footer><button type="button" data-close-marketing>Cancel</button><button type="submit">Create release</button></footer></form></dialog>
      {{end}}
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "integrations"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content integrations-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .IntegrationsAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">INTEGRATIONS PACKAGE</p><h1>Connect deliberately.<br><em>Observe every effect.</em></h1><p>Integrations becomes available when this Account has the package. Credentials remain in the secret broker and no provider payload is exposed here.</p><a href="/app#billing">Review Account plans →</a></div></section>
      {{else}}
      <section class="integrations-heading"><div><p class="eyebrow">INTEGRATIONS</p><h1>External effects,<br><em>held to evidence.</em></h1><p>Manage non-secret connector scope, inspect delivery health, and resolve uncertain provider outcomes without resending them.</p></div><div class="integrations-toolbar"><span class="work-mode">{{if .IntegrationsReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span><button id="integrations-refresh" type="button">Refresh</button>{{if not .IntegrationsReadOnly}}<button id="integrations-new-connection" type="button">New connection</button>{{end}}</div></section>
      <section class="integrations-app" id="integrations-app" data-account-id="{{.Selected.AccountID}}" data-user-id="{{.ActorUserID}}" data-read-only="{{.IntegrationsReadOnly}}" aria-busy="true">
        <p class="sr-only" id="integrations-command-status" role="status" aria-live="polite" aria-atomic="true"></p>
        <div class="integrations-layout">
          <section class="panel integrations-connections"><header><div><p class="eyebrow">CONNECTIONS</p><h2>Scope and authority</h2></div><label><span class="sr-only">Connection state</span><select id="integrations-connection-state"><option value="">All states</option><option value="pending">Pending</option><option value="active">Active</option><option value="disabled">Disabled</option><option value="revoked">Revoked</option></select></label></header><p id="integrations-connection-status" class="integrations-status" role="status">Loading connections…</p><div id="integrations-connection-list" class="integrations-list"></div></section>
          <section class="integrations-stage">
            <article class="panel integrations-detail" id="integrations-connection-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">CONNECTION DETAIL</p><h2>Select a connection</h2><p>Inspect exact non-secret scope, current binding generation, and latest health.</p></article>
            <section class="panel integrations-executions"><header><div><p class="eyebrow">EXTERNAL EXECUTIONS</p><h2>Delivery evidence</h2></div><div><label><span class="sr-only">Execution state</span><select id="integrations-execution-state"><option value="">All states</option><option value="prepared">Prepared</option><option value="executing">Executing</option><option value="reconciling">Reconciling</option><option value="retry_wait">Retry wait</option><option value="unknown">Unknown</option><option value="manual_resolution">Manual resolution</option><option value="succeeded">Succeeded</option><option value="failed">Failed</option><option value="cancelled">Cancelled</option></select></label>{{if not .IntegrationsReadOnly}}<button id="integrations-prepare-execution" type="button">Prepare delivery</button>{{end}}</div></header><p id="integrations-execution-status" class="integrations-status" role="status">Loading executions…</p><div id="integrations-execution-list" class="integrations-list"></div></section>
            <article class="panel integrations-detail integrations-execution-detail" id="integrations-execution-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">EXECUTION DETAIL</p><h2>Select an execution</h2><p>Inspect frozen authority, attempt outcomes, and any dual-controlled resolution.</p></article>
          </section>
        </div>
      </section>
      {{if not .IntegrationsReadOnly}}
      <dialog class="integrations-dialog" id="integrations-connection-dialog"><form id="integrations-connection-form"><header><div><p class="eyebrow">NON-SECRET SCOPE</p><h2 id="integrations-connection-form-title">New connection</h2></div><button type="button" data-close-integrations aria-label="Close">×</button></header><label>Name<input name="name" maxlength="160" required></label><div class="integrations-form-grid"><label>Connector<select name="kind"><option value="email">Email</option><option value="google_drive">Google Drive</option><option value="web_publish">Web publication</option></select></label><label>Capabilities<select name="capabilities" multiple required><option value="email.read">Email read</option><option value="email.send">Email send</option><option value="google_drive.read">Google Drive read</option><option value="web.publish">Web publish</option></select></label></div><label>Email address<input name="email_address" maxlength="320"></label><label>Audience reference<input name="audience_reference" maxlength="500"></label><label>Google Drive folder IDs, one per line<textarea name="drive_folder_ids" rows="4" maxlength="10049"></textarea></label><label>HTTPS origin<input name="https_origin" type="url" maxlength="500"></label><label>Path prefix<input name="path_prefix" maxlength="500"></label><p class="integrations-note">Provider credentials are not accepted in this form. Drive scope stores only canonical folder IDs, never folder names or OAuth material.</p><p class="integrations-error" role="alert" hidden></p><footer><button type="button" data-close-integrations>Cancel</button><button type="submit">Save scope</button></footer></form></dialog>
      <dialog class="integrations-dialog" id="integrations-credential-dialog"><form id="integrations-credential-form"><header><div><p class="eyebrow">SECRET-BROKER ATTESTATION</p><h2 id="integrations-credential-title">Activate binding</h2></div><button type="button" data-close-integrations aria-label="Close">×</button></header><input type="hidden" name="expected_generation" value="0"><label>Provider code<input name="provider" pattern="[a-z][a-z0-9_.]{0,63}" required></label><label>Reference SHA-256<input name="reference_sha256" pattern="[0-9a-f]{64}" required></label><label>Expires at<input name="expires_at" type="datetime-local"></label><p class="integrations-note">Enter only the reviewed digest of a broker reference. Never paste a key, token, password, or provider payload.</p><p class="integrations-error" role="alert" hidden></p><footer><button type="button" data-close-integrations>Cancel</button><button type="submit">Bind attestation</button></footer></form></dialog>
      <dialog class="integrations-dialog" id="integrations-execution-dialog"><form id="integrations-execution-form"><header><div><p class="eyebrow">FROZEN DELIVERY</p><h2>Prepare execution</h2></div><button type="button" data-close-integrations aria-label="Close">×</button></header><label>Approved release ID<input name="release_id" pattern="[0-9a-fA-F-]{36}" required></label><label>Release version<input name="release_version" type="number" min="1" required></label><div class="integrations-form-grid"><label>Capability<select name="capability"><option value="email.send">Email send</option><option value="web.publish">Web publish</option></select></label><label>Connection<select name="connection_id" required></select></label></div><p class="integrations-note">Preparation freezes authority and content digests; a worker performs the governed provider operation.</p><p class="integrations-error" role="alert" hidden></p><footer><button type="button" data-close-integrations>Cancel</button><button type="submit">Prepare delivery</button></footer></form></dialog>
      <dialog class="integrations-dialog" id="integrations-resolution-dialog"><form id="integrations-resolution-form"><header><div><p class="eyebrow">DUAL-CONTROLLED RECOVERY</p><h2>Propose external outcome</h2></div><button type="button" data-close-integrations aria-label="Close">×</button></header><input type="hidden" name="execution_id"><label>Observed outcome<select name="requested_outcome"><option value="succeeded">Applied successfully</option><option value="failed">Did not succeed</option></select></label><label>Evidence<textarea name="evidence" minlength="3" maxlength="1000" rows="4" required></textarea></label><p class="integrations-warning"><strong>This does not resend the operation.</strong> Spyglass stores only the evidence digest. A different Owner or Administrator must confirm the exact outcome.</p><p class="integrations-error" role="alert" hidden></p><footer><button type="button" data-close-integrations>Cancel</button><button type="submit">Request confirmation</button></footer></form></dialog>
      {{end}}
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "finance"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content finance-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .FinanceAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">FINANCE PACKAGE</p><h1>Keep the books clear.<br><em>Keep every posting governed.</em></h1><p>Finance is not included in this Account's current package set.</p><a href="/app#billing">Review Account plans →</a></div></section>
      {{else}}
      <section class="finance-heading"><div><p class="eyebrow">FINANCE</p><h1>A governed ledger<br><em>for the operating truth.</em></h1><p>Draft balanced journal entries, post them deliberately, and reconcile evidence without bypassing Account authority.</p></div><span class="work-mode">{{if .FinanceReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span></section>
      <section class="finance-app" id="finance-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.FinanceReadOnly}}" aria-busy="true">
        <p class="sr-only" id="finance-command-status" role="status" aria-live="polite" aria-atomic="true"></p>
        <div class="finance-toolbar panel">
          <label>LEDGER<select id="finance-ledger" aria-label="Active ledger"><option value="">Loading ledgers…</option></select></label>
          <button class="secondary" id="finance-refresh" type="button">Refresh</button>
          {{if not .FinanceReadOnly}}<button class="secondary" id="finance-manage-ledger" type="button" disabled>Manage ledger</button><button class="primary" id="finance-new-ledger" type="button">New ledger</button>{{end}}
        </div>
        <div class="finance-summary" role="region" aria-label="Ledger summary">
          <article><small>INCOME</small><strong id="finance-income">—</strong><span>Posted</span></article>
          <article><small>EXPENSE</small><strong id="finance-expense">—</strong><span>Posted</span></article>
          <article><small>NET</small><strong id="finance-net">—</strong><span>Income less expense</span></article>
          <article><small>DRAFTS</small><strong id="finance-drafts">—</strong><span>Awaiting posting</span></article>
        </div>
        <div class="finance-tabs" role="tablist" aria-label="Finance workspace">
          <button type="button" role="tab" aria-selected="true" aria-controls="finance-chart-panel" id="finance-chart-tab" data-finance-tab="chart">Chart of accounts</button>
          <button type="button" role="tab" aria-selected="false" aria-controls="finance-journal-panel" id="finance-journal-tab" data-finance-tab="journal">Journal</button>
          <button type="button" role="tab" aria-selected="false" aria-controls="finance-reconciliation-panel" id="finance-reconciliation-tab" data-finance-tab="reconciliation">Reconciliation</button>
        </div>
        <section class="finance-tab-panel" id="finance-chart-panel" role="tabpanel" aria-labelledby="finance-chart-tab">
          <div class="finance-layout">
            <section class="panel finance-list-panel"><header><div><p class="eyebrow">CHART OF ACCOUNTS</p><h2>Posting accounts</h2></div>{{if not .FinanceReadOnly}}<button class="secondary" id="finance-new-account" type="button">Add account</button>{{end}}</header><p class="finance-status" id="finance-accounts-status" role="status">Choose a ledger.</p><div class="finance-list" id="finance-accounts"></div></section>
            <aside class="panel finance-detail" id="finance-account-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">ACCOUNT DETAIL</p><h2>Select an account</h2><p>Inspect its normal balance, posting state, and current ledger balance.</p></aside>
          </div>
        </section>
        <section class="finance-tab-panel" id="finance-journal-panel" role="tabpanel" aria-labelledby="finance-journal-tab" hidden>
          <div class="finance-layout">
            <section class="panel finance-list-panel"><header><div><p class="eyebrow">GENERAL JOURNAL</p><h2>Entries</h2></div>{{if not .FinanceReadOnly}}<button class="secondary" id="finance-new-entry" type="button">Draft entry</button>{{end}}</header><div class="finance-filters"><label><span class="sr-only">Entry state</span><select id="finance-entry-state"><option value="">All states</option><option value="draft">Draft</option><option value="posted">Posted</option><option value="reversed">Reversed</option></select></label></div><p class="finance-status" id="finance-entries-status" role="status">Choose a ledger.</p><div class="finance-list" id="finance-entries"></div></section>
            <aside class="panel finance-detail" id="finance-entry-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">ENTRY DETAIL</p><h2>Select an entry</h2><p>Inspect balanced lines, provenance, evidence, and posting state.</p></aside>
          </div>
        </section>
        <section class="finance-tab-panel" id="finance-reconciliation-panel" role="tabpanel" aria-labelledby="finance-reconciliation-tab" hidden>
          <div class="finance-layout">
            <section class="panel finance-list-panel"><header><div><p class="eyebrow">RECONCILIATION</p><h2>Statement checks</h2></div>{{if not .FinanceReadOnly}}<button class="secondary" id="finance-new-reconciliation" type="button">Reconcile</button>{{end}}</header><p class="finance-status" id="finance-reconciliations-status" role="status">Choose a ledger.</p><div class="finance-list" id="finance-reconciliations"></div></section>
            <aside class="panel finance-detail" id="finance-reconciliation-detail" tabindex="-1" aria-live="polite"><p class="eyebrow">RECONCILIATION DETAIL</p><h2>Select a check</h2><p>Compare statement evidence with the immutable ledger balance.</p></aside>
          </div>
        </section>
      </section>
      {{if not .FinanceReadOnly}}
      <dialog class="finance-dialog" id="finance-ledger-dialog" aria-labelledby="finance-ledger-title"><form id="finance-ledger-form"><header><div><p class="eyebrow">LEDGER</p><h2 id="finance-ledger-title">Establish a book</h2></div><button type="button" data-close-dialog aria-label="Close">×</button></header><label>Name<input name="name" maxlength="160" required></label><div class="finance-form-grid"><label>Code<input name="code" maxlength="40" required></label><label>Currency<input name="currency" value="USD" minlength="3" maxlength="3" pattern="[A-Za-z]{3}" required></label></div><label>Description<textarea name="description" maxlength="4000" rows="3"></textarea></label><section class="finance-ledger-governance" id="finance-ledger-governance" hidden><p class="eyebrow">PERIOD GOVERNANCE</p><div class="finance-form-grid"><label>Close through<input type="date" name="close_through"></label><label>Evidence ID<input name="close_evidence" pattern="[0-9a-fA-F-]{36}"></label></div><div><button class="secondary" id="finance-close-period" type="button">Close period</button><button class="danger" id="finance-archive-ledger" type="button">Archive ledger</button></div></section><p class="finance-form-error" role="alert" hidden></p><footer><button class="secondary" type="button" data-close-dialog>Cancel</button><button class="primary" id="finance-save-ledger" type="submit">Create ledger</button></footer></form></dialog>
      <dialog class="finance-dialog" id="finance-account-dialog" aria-labelledby="finance-account-title"><form id="finance-account-form"><header><div><p class="eyebrow">CHART OF ACCOUNTS</p><h2 id="finance-account-title">Add posting account</h2></div><button type="button" data-close-dialog aria-label="Close">×</button></header><div class="finance-form-grid"><label>Code<input name="code" maxlength="40" required></label><label>Type<select name="type"><option value="asset">Asset</option><option value="liability">Liability</option><option value="equity">Equity</option><option value="income">Income</option><option value="expense">Expense</option></select></label></div><label>Name<input name="name" maxlength="160" required></label><label>Description<textarea name="description" maxlength="4000" rows="3"></textarea></label><label class="finance-check"><input type="checkbox" name="allow_posting" checked> Allow journal postings</label><p class="finance-form-error" role="alert" hidden></p><footer><button class="secondary" type="button" data-close-dialog>Cancel</button><button class="primary" type="submit">Add account</button></footer></form></dialog>
      <dialog class="finance-dialog finance-wide-dialog" id="finance-entry-dialog" aria-labelledby="finance-entry-title"><form id="finance-entry-form"><header><div><p class="eyebrow">JOURNAL DRAFT</p><h2 id="finance-entry-title">Record a balanced entry</h2></div><button type="button" data-close-dialog aria-label="Close">×</button></header><div class="finance-form-grid"><label>Entry date<input type="date" name="entry_date" required></label><label>Reference<input name="reference" maxlength="500"></label></div><label>Description<textarea name="description" maxlength="4000" rows="2" required></textarea></label><div class="finance-entry-lines"><label>Debit account<select name="debit_account" required></select></label><label>Credit account<select name="credit_account" required></select></label><label>Amount<input type="number" name="amount" min="0.01" step="0.01" required></label><label>Line memo<input name="memo" maxlength="1000"></label></div><label>Knowledge evidence IDs <input name="evidence" placeholder="Optional UUIDs separated by commas"></label><p class="finance-form-note">Creating an entry produces a draft. Posting is a separate, version-bound action.</p><p class="finance-form-error" role="alert" hidden></p><footer><button class="secondary" type="button" data-close-dialog>Cancel</button><button class="primary" type="submit">Create draft</button></footer></form></dialog>
      <dialog class="finance-dialog" id="finance-reconciliation-dialog" aria-labelledby="finance-reconciliation-title"><form id="finance-reconciliation-form"><header><div><p class="eyebrow">STATEMENT CHECK</p><h2 id="finance-reconciliation-title">Propose reconciliation</h2></div><button type="button" data-close-dialog aria-label="Close">×</button></header><label>Posting account<select name="posting_account_id" required></select></label><div class="finance-form-grid"><label>As of<input type="date" name="as_of" required></label><label>Statement balance<input type="number" name="statement_balance" step="0.01" required></label></div><label>Knowledge evidence ID<input name="evidence" pattern="[0-9a-fA-F-]{36}" required></label><p class="finance-form-error" role="alert" hidden></p><footer><button class="secondary" type="button" data-close-dialog>Cancel</button><button class="primary" type="submit">Compare balances</button></footer></form></dialog>
      {{end}}
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "your-turn"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content attention-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .AttentionAvailable}}
      <section class="work-locked panel"><div><p class="eyebrow">YOUR TURN</p><h1>Decisions gather here.<br><em>Nothing crosses Accounts.</em></h1><p>Your Turn becomes available with the Work or Agents package. It collects only questions and decisions this identity is authorized to inspect.</p><a href="/app#billing">Review Account plans →</a></div><div class="work-locked-map" aria-hidden="true"><i></i><i></i><i></i><b></b></div></section>
      {{else}}
      <section class="attention-heading"><div><p class="eyebrow">YOUR TURN</p><h1>The signal that needs<br><em>your judgment now.</em></h1><p>Answer missing information, review version-bound Work, and govern consequential Agent actions from one Account-scoped queue.</p></div><button class="secondary" id="attention-refresh" type="button">Refresh queue</button></section>
      <section class="attention-app" id="attention-app" data-account-id="{{.Selected.AccountID}}" data-user-id="{{.ActorUserID}}" data-work-available="{{.WorkAvailable}}" data-work-read-only="{{.WorkReadOnly}}" data-approvals-available="{{.ApprovalsAvailable}}" data-agents-read-only="{{.AgentsReadOnly}}" aria-busy="true">
        <p class="sr-only" id="attention-command-status" role="status" aria-live="polite" aria-atomic="true"></p>
        <div class="attention-summary" role="region" aria-label="Your Turn summary">
          <article><small>OPEN</small><strong data-attention-summary="all">—</strong><span>Total decisions</span></article>
          <article><small>INFORMATION</small><strong data-attention-summary="information">—</strong><span>Facts requested</span></article>
          <article><small>REVIEWS</small><strong data-attention-summary="review">—</strong><span>Work decisions</span></article>
          <article><small>APPROVALS</small><strong data-attention-summary="approval">—</strong><span>Consequential actions</span></article>
          <article><small>RECOVERY</small><strong data-attention-summary="action">—</strong><span>Action outcomes</span></article>
        </div>
        <div class="attention-layout">
          <section class="attention-queue panel" aria-labelledby="attention-queue-title">
            <header><div><p class="eyebrow">ACCOUNT QUEUE</p><h2 id="attention-queue-title">Waiting for you</h2></div><span id="attention-result-count">Loading</span></header>
            <div class="attention-tabs" role="group" aria-label="Filter Your Turn queue">
              <button type="button" data-attention-filter="all" aria-pressed="true">All</button>
              {{if .WorkAvailable}}<button type="button" data-attention-filter="information" aria-pressed="false">Information</button><button type="button" data-attention-filter="review" aria-pressed="false">Reviews</button>{{end}}
              {{if .ApprovalsAvailable}}<button type="button" data-attention-filter="approval" aria-pressed="false">Approvals</button><button type="button" data-attention-filter="action" aria-pressed="false">Recovery</button>{{end}}
            </div>
            <div class="attention-status" id="attention-status" role="status">Loading Your Turn…</div>
            <div class="attention-list" id="attention-list" role="region" aria-label="Items waiting for your attention"></div>
          </section>
          <aside class="attention-detail panel" id="attention-detail" tabindex="-1" aria-live="polite">
            <div class="attention-detail-empty"><p class="eyebrow">DECISION DETAIL</p><h2>Select an item</h2><p>Choose a queue item to inspect the exact question, proposal version, consequential payload, or redacted recovery status before acting.</p></div>
          </aside>
        </div>
      </section>
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "agents"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content agents-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .AgentsAvailable}}
      <section class="agents-locked panel"><div><p class="eyebrow">AGENTS PACKAGE</p><h1>Convene the right minds.<br><em>Keep every action governed.</em></h1><p>Agents is not included in this Account's current package set. The Operating plan adds versioned specialists, coordinated Boardrooms, and Account-bound runs.</p><a href="/app#billing">Review Account plans →</a></div><div class="agents-locked-orbit" aria-hidden="true"><i></i><i></i><i></i><b>IO</b></div></section>
      {{else}}
      <section class="agents-heading"><div><p class="eyebrow">AGENTS</p><h1>A boardroom for<br><em>the question at hand.</em></h1><p>Bring governed specialists into one Account-scoped conversation. Every run freezes its Persona versions, policy, tools, and entitlement boundary.</p></div><span class="work-mode">{{if .AgentsReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span></section>
      <section class="agents-app" id="agents-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.AgentsReadOnly}}" aria-busy="true">
        <aside class="agents-rail panel">
          <header><div><p class="eyebrow">BOARDROOMS</p><h2>Operating rooms</h2></div><span id="agents-room-count">Loading</span></header>
          <div class="agents-status" id="agents-room-status" role="status">Loading Boardrooms…</div>
          <nav id="agents-room-list" aria-label="Agent Boardrooms"></nav>
        </aside>
        <section class="agents-stage">
          <section class="agents-roster panel">
            <header><div><p class="eyebrow">CURRENT BOARDROOM</p><h2 id="agents-room-name">Select a Boardroom</h2><p id="agents-room-purpose">Choose an operating room to inspect its team and conversations.</p></div><span id="agents-room-policy"></span></header>
            <div id="agents-personas" class="agents-personas" role="region" aria-label="Boardroom Personas"></div>
            {{if not .AgentsReadOnly}}<form id="agents-manager-form" class="agents-manager-form" hidden><label>Synthesis manager<select id="agents-manager-persona" name="manager_persona_id" required></select></label><button type="submit">Set manager</button><p id="agents-manager-error" class="agents-form-error" role="alert" hidden></p></form>{{end}}
          </section>
          {{if not .AgentsReadOnly}}<form class="agents-composer panel" id="agents-run-form" hidden>
            <header><div><p class="eyebrow" id="agents-compose-label">NEW CONVERSATION</p><h2 id="agents-compose-title">Convene this Boardroom</h2></div><button class="agents-new-conversation" id="agents-new-conversation" type="button" hidden>New conversation</button></header>
            <label id="agents-subject-field">Subject<input name="subject" minlength="2" maxlength="240" required placeholder="What decision or situation needs a clear view?"></label>
            <label>Your question<textarea name="prompt" maxlength="65536" required rows="5" placeholder="Give the Boardroom the context, constraints, and outcome you need."></textarea></label>
            <label>Run mode<select id="agents-run-mode" name="mode"><option value="selected">Selected Personas</option><option value="manager_led">Specialists, then synthesis manager</option></select></label>
            <fieldset><legend>Invite Personas</legend><div id="agents-persona-picker"></div></fieldset>
            <p class="agents-form-error" id="agents-form-error" role="alert" hidden></p>
            <footer><span>Runs are immutable, capacity-governed, and Account-bound.</span><button type="submit">Convene Boardroom →</button></footer>
          </form>{{end}}
          <section class="agents-conversations panel">
            <header><div><p class="eyebrow">CONVERSATIONS</p><h2>Decision history</h2></div><span id="agents-conversation-count">Select a room</span></header>
            <div id="agents-conversation-status" class="agents-status" role="status">No Boardroom selected.</div>
            <div id="agents-conversation-list" role="region" aria-label="Boardroom conversations"></div>
            <button id="agents-more-conversations" class="agents-more" type="button" hidden>Load more conversations</button>
          </section>
          <section class="agents-transcript panel" id="agents-transcript" hidden>
            <header><div><p class="eyebrow">CONVERSATION</p><h2 id="agents-transcript-title"></h2></div><span id="agents-transcript-state"></span></header>
            <div id="agents-run-status" class="agents-run-status" role="status" hidden></div>
            {{if not .AgentsReadOnly}}<form id="agents-run-recovery" class="agents-run-recovery" hidden>
              <div><p class="eyebrow">RUN RECOVERY</p><h3 id="agents-recovery-title">Resolve this run</h3><p id="agents-recovery-summary"></p></div>
              <label>Resolution note<textarea id="agents-recovery-note" name="note" minlength="3" maxlength="1000" required rows="3" placeholder="Record why this outcome is being retried or accepted."></textarea></label>
              <p id="agents-recovery-error" class="agents-form-error" role="alert" hidden></p>
              <footer><button type="submit" name="action" value="retry_failed">Retry failed turns</button><button class="secondary" type="submit" name="action" value="accept_failure">Accept failure</button></footer>
            </form>{{end}}
            <div id="agents-message-list" role="log" aria-label="Conversation messages"></div>
            <button id="agents-more-messages" class="agents-more" type="button" hidden>Load later messages</button>
          </section>
        </section>
      </section>
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "schedules"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content schedules-content">
      {{template "alert" .}}
      {{if not .Selected}}
      <section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>
      {{else if not .AgentsAvailable}}
      <section class="agents-locked panel"><div><p class="eyebrow">AGENTS PACKAGE</p><h1>Put governed work<br><em>on a dependable rhythm.</em></h1><p>Schedules use the Agents package because every occurrence enters the same governed Boardroom Run boundary.</p><a href="/app#billing">Review Account plans →</a></div><div class="agents-locked-orbit" aria-hidden="true"><i></i><i></i><i></i><b>IO</b></div></section>
      {{else}}
      <section class="schedules-heading"><div><p class="eyebrow">SCHEDULES</p><h1>Give recurring work<br><em>a precise local time.</em></h1><p>Every occurrence creates a fresh, capacity-governed Agent conversation. Timezone, daylight-saving behavior, missed runs, Personas, and context remain explicit.</p></div><span class="work-mode">{{if .AgentsReadOnly}}READ-ONLY ACCESS{{else}}PACKAGE ENABLED{{end}}</span></section>
      <section class="schedules-app" id="schedules-app" data-account-id="{{.Selected.AccountID}}" data-read-only="{{.AgentsReadOnly}}" aria-busy="true">
        <section class="schedules-list panel">
          <header><div><p class="eyebrow">DEFINITIONS</p><h2>Recurring Agent runs</h2></div><span id="schedules-count">Loading</span></header>
          <div id="schedules-status" class="schedule-status" role="status">Loading Schedules…</div>
          <div id="schedules-list" role="region" aria-label="Customer Schedules"></div>
          <button id="schedules-more" class="schedules-more" type="button" hidden>Load more Schedules</button>
        </section>
        {{if not .AgentsReadOnly}}
        <form id="schedule-form" class="schedule-form panel">
          <header><div><p class="eyebrow" id="schedule-form-label">NEW SCHEDULE</p><h2 id="schedule-form-title">Create a recurring Run</h2></div><button id="schedule-cancel" class="secondary" type="button" hidden>Cancel edit</button></header>
          <input id="schedule-version" name="expected_version" type="hidden">
          <div class="schedule-form-grid">
            <label>Name<input name="name" minlength="2" maxlength="160" required placeholder="Daily operating review"></label>
            <label>IANA timezone<input name="timezone" maxlength="200" required placeholder="America/New_York"></label>
            <label>Frequency<select name="frequency"><option value="daily">Daily</option><option value="weekly">Weekly</option></select></label>
            <label>Local time<input name="local_time" type="time" value="09:00" required></label>
            <label>DST gap<select name="gap_policy"><option value="skip">Skip missing wall time</option><option value="next_valid">Use next valid time</option></select></label>
            <label>DST overlap<select name="overlap_policy"><option value="first">First occurrence</option><option value="second">Second occurrence</option></select></label>
            <label>Missed runs<select name="missed_run_policy"><option value="skip">Skip backlog</option><option value="catch_up_one">Catch up one</option></select></label>
            <label>Boardroom<select id="schedule-boardroom" name="boardroom_id" required><option value="">Loading Boardrooms…</option></select></label>
            <label>Run mode<select name="mode"><option value="selected">Selected Personas</option><option value="manager_led">Specialists, then manager</option></select></label>
          </div>
          <fieldset id="schedule-weekdays" hidden><legend>Weekly days</legend><div class="schedule-checks"><label><input type="checkbox" value="1">Mon</label><label><input type="checkbox" value="2">Tue</label><label><input type="checkbox" value="3">Wed</label><label><input type="checkbox" value="4">Thu</label><label><input type="checkbox" value="5">Fri</label><label><input type="checkbox" value="6">Sat</label><label><input type="checkbox" value="0">Sun</label></div></fieldset>
          <fieldset><legend>Personas</legend><div id="schedule-personas" class="schedule-checks"><p>Select a Boardroom.</p></div></fieldset>
          <label>Conversation subject<input name="subject" minlength="2" maxlength="240" required placeholder="Daily operating review"></label>
          <label>Run prompt<textarea name="prompt" maxlength="65536" rows="5" required placeholder="Review the current priorities, risks, and next actions."></textarea></label>
          <details><summary>Attach Account context by ID</summary><div class="schedule-form-grid"><label>Work items<input name="work_item_ids" placeholder="UUIDs separated by commas"></label><label>Knowledge facts<input name="knowledge_fact_ids" placeholder="UUIDs separated by commas"></label><label>Knowledge documents<input name="knowledge_document_ids" placeholder="UUIDs separated by commas"></label><label>Baseline assessments<input name="baseline_assessment_ids" placeholder="UUIDs separated by commas"></label></div></details>
          <label>Change reason<input name="reason" maxlength="500" placeholder="Why this Schedule is being created or revised"></label>
          <p id="schedule-form-error" class="agents-form-error" role="alert" hidden></p>
          <footer><span>Optimistic versions prevent one browser from overwriting another.</span><button type="submit" id="schedule-submit">Create Schedule →</button></footer>
        </form>
        {{end}}
      </section>
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}

{{define "closures"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content lifecycle-content">
      {{template "alert" .}}
      <section class="lifecycle-heading"><div><p class="eyebrow">ACCOUNT LIFECYCLE</p><h1>Keep closure<br><em>deliberate and recoverable.</em></h1><p>This identity-wide surface remains available even when an Account is frozen. It is the recovery path for Accounts you own.</p></div><a href="/app">Return to active Accounts</a></section>
      <section class="lifecycle-list panel"><header><div><p class="eyebrow">OWNED ACCOUNTS</p><h2>Closure history</h2></div><span>{{len .Closures}} records</span></header>
        <div>{{range .Closures}}<article class="lifecycle-record">
          <div><small>{{.AccountState}} ACCOUNT</small><h3>{{.AccountName}}</h3><p>{{.Reason}}</p><dl><div><dt>Requested</dt><dd>{{.RequestedAt.Format "02 Jan 2006 15:04 UTC"}}</dd></div><div><dt>Closure eligible</dt><dd>{{.ExecuteAfter.Format "02 Jan 2006 15:04 UTC"}}</dd></div>{{if .DeleteAfter}}<div><dt>Retention deadline</dt><dd>{{.DeleteAfter.Format "02 Jan 2006 15:04 UTC"}}</dd></div>{{end}}</dl></div>
          <aside><strong class="lifecycle-state {{.State}}">{{.State}}</strong>{{if .BlockerCode}}<p>Blocked: {{.BlockerCode}}</p>{{end}}{{if or (eq .State "cooling_off") (eq .State "blocked") (eq .State "processing")}}<form method="post" action="/app/account-closures/cancel"><input type="hidden" name="account_id" value="{{.AccountID}}"><input type="hidden" name="account_version" value="{{.AccountVersion}}"><input type="hidden" name="reason" value="Owner canceled Account closure"><label>Type RESTORE<input name="confirmation" pattern="RESTORE" autocomplete="off" required></label><button type="submit">Restore Account</button></form>{{end}}</aside>
        </article>{{else}}<div class="lifecycle-empty"><h3>No closure history</h3><p>Accounts you own remain active. Closure requests and retention deadlines will appear here.</p></div>{{end}}</div>
      </section>
      <p class="lifecycle-footnote">Closed means normal Account access is permanently disabled. Physical data erasure is intentionally handled by a separate, audited operator workflow after the displayed retention deadline.</p>
    </div>
  </main>
</div></body></html>
{{end}}

{{define "account-exports"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace" id="main-content" tabindex="-1">
    {{template "private-topbar" .}}
    <div class="content lifecycle-content">
      {{template "alert" .}}
      <section class="lifecycle-heading"><div><p class="eyebrow">ACCOUNT PORTABILITY</p><h1>Take a governed<br><em>snapshot of your Account.</em></h1><p>Exports combine the global Account record with its current cell data, then retain one immutable ZIP for seven days.</p></div><a href="/app/security">Confirm with a passkey</a></section>
      {{if not .Selected}}<section class="empty"><h2>No Account selected</h2><p>Select an Account before viewing export history.</p></section>
      {{else if not .CanManageExports}}<section class="panel"><header><div><p class="eyebrow">OWNER ACCESS</p><h2>Exports are owner-managed</h2></div></header><p>Only an active owner with completed security enrollment can request, cancel, or download a whole-Account export.</p></section>
      {{else}}
      <section class="panel"><header><div><p class="eyebrow">NEW EXPORT</p><h2>Request Account snapshot</h2></div><span>7-day retention</span></header><p>Requesting an export requires a passkey confirmation from the last 10 minutes. Only one build may be active for an Account.</p><form method="post" action="/app/account-exports/request"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><label>Type EXPORT<input name="confirmation" pattern="EXPORT" autocomplete="off" required></label><button type="submit">Request export</button></form></section>
      <section class="lifecycle-list panel"><header><div><p class="eyebrow">EXPORT HISTORY</p><h2>Requests and artifacts</h2></div><span>{{len .Exports}} records</span></header><div>{{range .Exports}}<article class="lifecycle-record"><div><small>REQUEST {{.ID}}</small><h3>{{.State}}</h3><p>Requested {{.RequestedAt.Format "02 Jan 2006 15:04 UTC"}} · version {{.Version}}</p><dl><div><dt>Expires</dt><dd>{{.ExpiresAt.Format "02 Jan 2006 15:04 UTC"}}</dd></div>{{if .ArtifactBytes}}<div><dt>Artifact bytes</dt><dd>{{.ArtifactBytes}}</dd></div>{{end}}{{if .ErrorCode}}<div><dt>Result</dt><dd>{{.ErrorCode}}</dd></div>{{end}}</dl></div><aside>{{if eq .State "available"}}<form method="post" action="/app/account-exports/download"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="export_id" value="{{.ID}}"><button type="submit">Download ZIP</button></form>{{else if eq .State "queued"}}<form method="post" action="/app/account-exports/cancel"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="export_id" value="{{.ID}}"><input type="hidden" name="version" value="{{.Version}}"><button type="submit">Cancel request</button></form>{{end}}</aside></article>{{else}}<div class="lifecycle-empty"><h3>No export history</h3><p>Requested Account snapshots will appear here.</p></div>{{end}}</div></section>
      <p class="lifecycle-footnote">Artifact object identity and content digest remain private. Every download is authorized against the current request version and exact immutable object.</p>
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}
`
