package browserapp

const pageTemplates = `
{{define "head"}}
<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · Infinite Ocean: Spyglass</title><meta name="description" content="Infinite Ocean: Spyglass business operating system">
<link rel="stylesheet" href="/assets/spyglass.css?v=2">{{if .Script}}<script src="{{.Script}}?v=2" defer></script>{{end}}</head><body><a class="skip-link" href="#main-content">Skip to main content</a>
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
    <nav aria-label="Primary navigation"><p>OPERATE</p><a {{if eq .Page "app"}}class="active" aria-current="page"{{end}} href="/app"><i aria-hidden="true">⌂</i>Overview</a><a {{if eq .Page "your-turn"}}class="active" aria-current="page"{{end}} href="/app/your-turn"><i aria-hidden="true">!</i>Your Turn</a><a {{if eq .Page "work"}}class="active" aria-current="page"{{end}} href="/app/work"><i aria-hidden="true">✓</i>Work</a><a {{if eq .Page "agents"}}class="active" aria-current="page"{{end}} href="/app/agents"><i aria-hidden="true">◌</i>Agents</a><a href="/app#knowledge"><i aria-hidden="true">◇</i>Knowledge</a><p>BUSINESS</p><a href="/app#finance"><i aria-hidden="true">≋</i>Finance</a><a href="/app#marketing"><i aria-hidden="true">↗</i>Marketing</a><a href="/app#billing"><i aria-hidden="true">$</i>Billing</a><a href="/app#settings"><i aria-hidden="true">⚙</i>Account</a><a {{if eq .Page "closures"}}class="active" aria-current="page"{{end}} href="/app/account-closures"><i aria-hidden="true">○</i>Lifecycle</a><a href="/app/security"><i aria-hidden="true">◇</i>Security</a></nav>
    <form method="post" action="/logout"><button class="logout" type="submit">Sign out</button></form>
  </aside>
{{end}}

{{define "private-topbar"}}
<header class="topbar"><div><strong>{{if .Selected}}{{.Selected.DisplayName}}{{else}}No Account selected{{end}}</strong><small>Infinite Ocean: Spyglass</small></div><span class="live"><i aria-hidden="true"></i>Account services ready</span></header>
{{end}}

{{define "login"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">WELCOME BACK</p><h1>Find the signal.<br><em>Move the business.</em></h1><p>Sign in once, then choose the Spyglass Account where you want to work.</p></div><footer>One identity · Explicit Account access · Package-aware</footer></section><section class="auth-panel"><form method="post" action="/login"><p class="eyebrow">SECURE ACCESS</p><h2>Sign in to Spyglass</h2>{{template "alert" .}}<input type="hidden" name="return_to" value="{{.ReturnTo}}"><label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><label>Password<input type="password" name="password" autocomplete="current-password" required></label><p class="form-note"><a href="/forgot-password">Forgot your password?</a></p><button type="submit">Sign in <span>→</span></button>{{if .PasskeysConfigured}}<div class="auth-divider"><span>or</span></div><button class="passkey-button secondary" id="passkey-login" type="button" data-return-to="{{.ReturnTo}}">Sign in with a passkey <span>◇</span></button><p class="passkey-status" id="passkey-status" role="status"></p>{{end}}<p class="form-note">New to Infinite Ocean? <a href="/signup">Create a free Account</a>.</p></form></section></main></body></html>
{{end}}

{{define "forgot"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY RECOVERY</p><h1>Restore access.<br><em>Keep every Account.</em></h1><p>Your Infinite Ocean identity spans Spyglass Accounts. Recover the identity once; no Account data or Membership is recreated.</p></div><footer>Single-use link · 30-minute expiry · Every session revoked</footer></section><section class="auth-panel"><form method="post" action="/forgot-password"><p class="eyebrow">RECOVERY LINK</p><h2>Find your identity</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development recovery</strong><a href="/reset-password?token={{.DevelopmentToken}}">Set a new password</a></div>{{else}}<label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><button type="submit">Send recovery link <span>→</span></button>{{end}}<p class="form-note"><a href="/login">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "reset"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">SECURE THE IDENTITY</p><h1>Set a new key<br><em>to the view ahead.</em></h1><p>Completing recovery invalidates the link, changes the password, and signs the identity out everywhere.</p></div><footer>Argon2id credential · Security version advanced · Sessions revoked</footer></section><section class="auth-panel"><form method="post" action="/reset-password"><p class="eyebrow">NEW PASSWORD</p><h2>Reset your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><label>New password<input type="password" name="password" autocomplete="new-password" minlength="12" aria-describedby="password-requirements" required><small id="password-requirements">Use at least 12 characters.</small></label><button type="submit">Update password <span>→</span></button><p class="form-note"><a href="/login">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "signup"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">START WITH CLARITY</p><h1>Your first clear<br><em>view is free.</em></h1><p>Create a system-wide identity and a real Spyglass Account. Payment information is optional.</p></div><footer>Free plan · No customer container · Upgrade by package</footer></section><section class="auth-panel"><form method="post" action="/signup"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h2>Create your Account</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development verification</strong><a href="/verify?token={{.DevelopmentToken}}{{if .OfferCode}}&offer={{.OfferCode}}{{end}}">Continue to password setup</a></div>{{else}}<label>Your name<input name="name" value="{{.Name}}" autocomplete="name" required></label><label>Work email<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><label>Business name<input name="account_name" value="{{.AccountName}}" autocomplete="organization" required></label><input type="hidden" name="region" value="us-east">{{if .OfferCode}}<input type="hidden" name="offer_code" value="{{.OfferCode}}">{{end}}<button type="submit">Continue securely <span>→</span></button>{{end}}<p class="form-note">Already registered? <a href="/login">Sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "verify"}}
{{template "head" .}}<main class="auth" id="main-content" tabindex="-1"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY VERIFIED</p><h1>Secure the<br><em>view ahead.</em></h1><p>Your password protects your Infinite Ocean identity across every Account you are invited to join.</p></div><footer>12+ characters · Rotating sessions · Account isolation</footer></section><section class="auth-panel"><form method="post" action="/verify"><p class="eyebrow">FINAL STEP</p><h2>Choose your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}">{{if .OfferCode}}<input type="hidden" name="offer_code" value="{{.OfferCode}}">{{end}}<label>Password<input type="password" name="password" autocomplete="new-password" minlength="12" aria-describedby="password-requirements" required><small id="password-requirements">Use at least 12 characters.</small></label><button type="submit">Create identity and Account <span>→</span></button></form></section></main></body></html>
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
  <header>{{template "brand" .}}<a href="/app">Return to Spyglass</a></header>
  <section class="security-hero"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h1>Security follows<br><em>you, not an Account.</em></h1><p>Review every active Spyglass session, manage passkeys, or confirm your identity before a sensitive change.</p></section>
  <div class="security-grid">
    <section class="security-card"><p class="eyebrow">IDENTITY CONFIRMATION</p><h2>Unlock sensitive actions</h2>{{template "alert" .}}<p>Password confirmation unlocks factor recovery. A user-verified passkey unlocks verified-email changes and privileged Account actions—including Membership, invitation, and billing changes—for 10 minutes on this session.</p><form method="post" action="/app/security/reauthenticate"><label>Current password<input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Confirm password</button></form>{{if .PasskeysConfigured}}{{if .Passkeys}}<div class="security-or"><span>or</span></div><button class="passkey-button secondary" id="passkey-reauthenticate" type="button">Unlock with a passkey</button>{{end}}<p class="passkey-status" id="passkey-status" role="status"></p>{{end}}</section>
    {{if .ContactChangesConfigured}}<section class="security-card identity-contact"><p class="eyebrow">VERIFIED CONTACT</p><h2>Identity email</h2><p>Your current login is <strong>{{.CurrentEmail}}</strong>. Changing it requires a recent passkey confirmation and verification from the new mailbox. Every session is revoked when the change completes.</p><form method="post" action="/app/security/contact-change"><label>New email<input type="email" name="new_email" value="{{.NewEmail}}" autocomplete="email" required></label><button type="submit">Send verification</button></form>{{if .DevelopmentToken}}<div class="dev-link"><strong>Development email verification</strong><a href="/contact-change/verify?token={{.DevelopmentToken}}">Verify the new email</a></div>{{end}}</section>{{end}}
    <section class="security-card"><div class="security-card-head"><div><p class="eyebrow">ACTIVE SESSIONS</p><h2>Where you are signed in</h2></div><form method="post" action="/app/security/sessions/revoke-all"><button class="danger" type="submit">Sign out everywhere</button></form></div><div class="session-list">{{range .ActiveSessions}}<article><div><strong>{{.ClientLabel}}</strong>{{if .Current}}<em>Current session</em>{{end}}<small>Signed in with {{.AuthenticationMethod}} · Last confirmed with {{.ReauthenticationMethod}}</small><small>Last used {{.LastSeenAt.Format "Jan 2, 2006 at 15:04 UTC"}} · Expires {{.ExpiresAt.Format "Jan 2, 2006"}}</small></div><form method="post" action="/app/security/sessions/revoke"><input type="hidden" name="session_id" value="{{.ID}}"><button type="submit">{{if .Current}}Sign out{{else}}Revoke{{end}}</button></form></article>{{else}}<p>No active sessions.</p>{{end}}</div></section>
    <section class="security-card factor-loss-policy"><p class="eyebrow">FACTOR-LOSS POLICY</p><h2>Know the last-resort path</h2><ol><li><b>1</b><span><strong>Recover the password if needed</strong><small>The email recovery link changes the password and signs every session out.</small></span></li><li><b>2</b><span><strong>Use one saved recovery code</strong><small>It unlocks replacement-passkey enrollment only for this User and session, for 10 minutes.</small></span></li><li><b>3</b><span><strong>Add a passkey and replace the code set</strong><small>Owner authority stays closed until a passkey and at least one unused code both exist.</small></span></li></ol><p class="factor-loss-warning"><strong>Infinite Ocean support cannot view or recreate recovery codes, impersonate a passkey, or mark an owner ready.</strong> If every authenticator and every saved code are unavailable, self-service owner recovery is not possible and Account authority remains locked.</p></section>
    {{if .PasskeysConfigured}}<section class="security-card security-passkeys"><div class="security-card-head"><div><p class="eyebrow">PASSKEYS</p><h2>Phishing-resistant sign-in</h2></div></div><p>Passkeys belong to your Infinite Ocean identity and work across every Spyglass Account you can access. Confirm with an existing passkey before adding another. If every passkey is lost, confirm your password and use one saved recovery code.</p><div class="passkey-list">{{range .Passkeys}}<article><div><strong>{{.Name}}</strong><small>Added {{.CreatedAt.Format "Jan 2, 2006"}}{{if .LastUsedAt}} · Last used {{.LastUsedAt.Format "Jan 2, 2006"}}{{end}}{{if .BackedUp}} · Synced{{end}}</small></div><div><button class="passkey-rename secondary" type="button" data-credential-id="{{.ID}}" data-credential-name="{{.Name}}">Rename</button><button class="passkey-remove" type="button" data-credential-id="{{.ID}}">Remove</button><button class="passkey-compromise secondary" type="button" data-credential-id="{{.ID}}">Report compromised</button></div></article>{{else}}<p>No passkeys have been added.</p>{{end}}</div><div class="passkey-enroll"><label>Passkey name<input id="passkey-name" maxlength="80" value="My passkey" autocomplete="off"></label><button id="passkey-register" type="button">Add passkey</button></div></section>{{end}}
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
        </div>
        <div class="attention-layout">
          <section class="attention-queue panel" aria-labelledby="attention-queue-title">
            <header><div><p class="eyebrow">ACCOUNT QUEUE</p><h2 id="attention-queue-title">Waiting for you</h2></div><span id="attention-result-count">Loading</span></header>
            <div class="attention-tabs" role="group" aria-label="Filter Your Turn queue">
              <button type="button" data-attention-filter="all" aria-pressed="true">All</button>
              {{if .WorkAvailable}}<button type="button" data-attention-filter="information" aria-pressed="false">Information</button><button type="button" data-attention-filter="review" aria-pressed="false">Reviews</button>{{end}}
              {{if .ApprovalsAvailable}}<button type="button" data-attention-filter="approval" aria-pressed="false">Approvals</button>{{end}}
            </div>
            <div class="attention-status" id="attention-status" role="status">Loading Your Turn…</div>
            <div class="attention-list" id="attention-list" role="region" aria-label="Items waiting for your attention"></div>
          </section>
          <aside class="attention-detail panel" id="attention-detail" tabindex="-1" aria-live="polite">
            <div class="attention-detail-empty"><p class="eyebrow">DECISION DETAIL</p><h2>Select an item</h2><p>Choose a queue item to inspect the exact question, proposal version, or consequential payload before acting.</p></div>
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
          </section>
          {{if not .AgentsReadOnly}}<form class="agents-composer panel" id="agents-run-form" hidden>
            <header><div><p class="eyebrow" id="agents-compose-label">NEW CONVERSATION</p><h2 id="agents-compose-title">Convene this Boardroom</h2></div><button class="agents-new-conversation" id="agents-new-conversation" type="button" hidden>New conversation</button></header>
            <label id="agents-subject-field">Subject<input name="subject" minlength="2" maxlength="240" required placeholder="What decision or situation needs a clear view?"></label>
            <label>Your question<textarea name="prompt" maxlength="65536" required rows="5" placeholder="Give the Boardroom the context, constraints, and outcome you need."></textarea></label>
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
`
