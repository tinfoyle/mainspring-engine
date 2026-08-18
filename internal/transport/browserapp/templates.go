package browserapp

const pageTemplates = `
{{define "head"}}
<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · Infinite Ocean: Spyglass</title><meta name="description" content="Infinite Ocean: Spyglass business operating system">
<link rel="stylesheet" href="/assets/spyglass.css">{{if .Script}}<script src="{{.Script}}" defer></script>{{end}}</head><body>
{{end}}

{{define "brand"}}
<a class="brand" href="https://infiniteocean.net" aria-label="Infinite Ocean home"><i aria-hidden="true"><b></b></i><span><strong>INFINITE OCEAN</strong><small>SPYGLASS</small></span></a>
{{end}}

{{define "alert"}}
{{if .Error}}<div class="alert error" role="alert">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="alert success" role="status">{{.Notice}}</div>{{end}}
{{end}}

{{define "private-sidebar"}}
  <aside class="sidebar">
    {{template "brand" .}}
    <form class="account-switch" method="post" action="/app/account">
      <label>ACTIVE ACCOUNT<select name="account_id">{{range .Choices}}<option value="{{.AccountID}}" {{if $.Selected}}{{if eq .AccountID $.Selected.AccountID}}selected{{end}}{{end}}>{{.DisplayName}}</option>{{end}}</select></label>
      <button type="submit">Switch Account</button>
    </form>
    <nav><p>OPERATE</p><a {{if eq .Page "app"}}class="active"{{end}} href="/app"><i>⌂</i>Overview</a><a {{if eq .Page "work"}}class="active"{{end}} href="/app/work"><i>✓</i>Work</a><a href="/app#agents"><i>◌</i>Agents</a><a href="/app#knowledge"><i>◇</i>Knowledge</a><p>BUSINESS</p><a href="/app#finance"><i>≋</i>Finance</a><a href="/app#marketing"><i>↗</i>Marketing</a><a href="/app#billing"><i>$</i>Billing</a><a href="/app#settings"><i>⚙</i>Account</a><a href="/app/security"><i>◇</i>Security</a></nav>
    <form method="post" action="/logout"><button class="logout">Sign out</button></form>
  </aside>
{{end}}

{{define "private-topbar"}}
<header class="topbar"><div><strong>{{if .Selected}}{{.Selected.DisplayName}}{{else}}No Account selected{{end}}</strong><small>Infinite Ocean: Spyglass</small></div><span class="live"><i></i>Account services ready</span></header>
{{end}}

{{define "login"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">WELCOME BACK</p><h1>Find the signal.<br><em>Move the business.</em></h1><p>Sign in once, then choose the Spyglass Account where you want to work.</p></div><footer>One identity · Explicit Account access · Package-aware</footer></section><section class="auth-panel"><form method="post" action="/login"><p class="eyebrow">SECURE ACCESS</p><h2>Sign in to Spyglass</h2>{{template "alert" .}}<input type="hidden" name="return_to" value="{{.ReturnTo}}"><label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required autofocus></label><label>Password<input type="password" name="password" autocomplete="current-password" required></label><p class="form-note"><a href="/forgot-password">Forgot your password?</a></p><button type="submit">Sign in <span>→</span></button>{{if .PasskeysConfigured}}<div class="auth-divider"><span>or</span></div><button class="passkey-button secondary" id="passkey-login" type="button" data-return-to="{{.ReturnTo}}">Sign in with a passkey <span>◇</span></button><p class="passkey-status" id="passkey-status" role="status"></p>{{end}}<p class="form-note">New to Infinite Ocean? <a href="/signup">Create a free Account</a>.</p></form></section></main></body></html>
{{end}}

{{define "forgot"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY RECOVERY</p><h1>Restore access.<br><em>Keep every Account.</em></h1><p>Your Infinite Ocean identity spans Spyglass Accounts. Recover the identity once; no Account data or Membership is recreated.</p></div><footer>Single-use link · 30-minute expiry · Every session revoked</footer></section><section class="auth-panel"><form method="post" action="/forgot-password"><p class="eyebrow">RECOVERY LINK</p><h2>Find your identity</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development recovery</strong><a href="/reset-password?token={{.DevelopmentToken}}">Set a new password</a></div>{{else}}<label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required autofocus></label><button type="submit">Send recovery link <span>→</span></button>{{end}}<p class="form-note"><a href="/login">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "reset"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">SECURE THE IDENTITY</p><h1>Set a new key<br><em>to the view ahead.</em></h1><p>Completing recovery invalidates the link, changes the password, and signs the identity out everywhere.</p></div><footer>Argon2id credential · Security version advanced · Sessions revoked</footer></section><section class="auth-panel"><form method="post" action="/reset-password"><p class="eyebrow">NEW PASSWORD</p><h2>Reset your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><label>New password<input type="password" name="password" autocomplete="new-password" minlength="12" required><small>Use at least 12 characters.</small></label><button type="submit">Update password <span>→</span></button><p class="form-note"><a href="/login">Return to sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "signup"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">START WITH CLARITY</p><h1>Your first clear<br><em>view is free.</em></h1><p>Create a system-wide identity and a real Spyglass Account. Payment information is optional.</p></div><footer>Free plan · No customer container · Upgrade by package</footer></section><section class="auth-panel"><form method="post" action="/signup"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h2>Create your Account</h2>{{template "alert" .}}{{if .DevelopmentToken}}<div class="dev-link"><strong>Development verification</strong><a href="/verify?token={{.DevelopmentToken}}">Continue to password setup</a></div>{{else}}<label>Your name<input name="name" value="{{.Name}}" autocomplete="name" required></label><label>Work email<input type="email" name="email" value="{{.Email}}" autocomplete="email" required></label><label>Business name<input name="account_name" value="{{.AccountName}}" autocomplete="organization" required></label><input type="hidden" name="region" value="us-east"><button type="submit">Continue securely <span>→</span></button>{{end}}<p class="form-note">Already registered? <a href="/login">Sign in</a>.</p></form></section></main></body></html>
{{end}}

{{define "verify"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">IDENTITY VERIFIED</p><h1>Secure the<br><em>view ahead.</em></h1><p>Your password protects your Infinite Ocean identity across every Account you are invited to join.</p></div><footer>12+ characters · Rotating sessions · Account isolation</footer></section><section class="auth-panel"><form method="post" action="/verify"><p class="eyebrow">FINAL STEP</p><h2>Choose your password</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><label>Password<input type="password" name="password" autocomplete="new-password" minlength="12" required><small>Use at least 12 characters.</small></label><button type="submit">Create identity and Account <span>→</span></button></form></section></main></body></html>
{{end}}

{{define "accept"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">YOU'RE INVITED</p><h1>Bring another<br><em>Account into view.</em></h1><p>The Membership will be added to your existing Infinite Ocean identity.</p></div></section><section class="auth-panel"><form method="post" action="/invitations/accept"><p class="eyebrow">ACCOUNT MEMBERSHIP</p><h2>Accept invitation</h2>{{template "alert" .}}<input type="hidden" name="token" value="{{.Token}}"><button type="submit">Join this Account <span>→</span></button><p class="form-note"><a href="/app">Return to Spyglass</a></p></form></section></main></body></html>
{{end}}

{{define "security"}}
{{template "head" .}}
<main class="security-layout">
  <header>{{template "brand" .}}<a href="/app">Return to Spyglass</a></header>
  <section class="security-hero"><p class="eyebrow">INFINITE OCEAN IDENTITY</p><h1>Security follows<br><em>you, not an Account.</em></h1><p>Review every active Spyglass session, manage passkeys, or confirm your identity before a sensitive change.</p></section>
  <div class="security-grid">
    <section class="security-card"><p class="eyebrow">IDENTITY CONFIRMATION</p><h2>Unlock sensitive actions</h2>{{template "alert" .}}<p>Password confirmation unlocks identity settings. A user-verified passkey unlocks privileged Account actions—including Membership, invitation, and billing changes—for 10 minutes on this session.</p><form method="post" action="/app/security/reauthenticate"><label>Current password<input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Confirm password</button></form>{{if .PasskeysConfigured}}{{if .Passkeys}}<div class="security-or"><span>or</span></div><button class="passkey-button secondary" id="passkey-reauthenticate" type="button">Unlock with a passkey</button>{{end}}<p class="passkey-status" id="passkey-status" role="status"></p>{{end}}</section>
    <section class="security-card"><div class="security-card-head"><div><p class="eyebrow">ACTIVE SESSIONS</p><h2>Where you are signed in</h2></div><form method="post" action="/app/security/sessions/revoke-all"><button class="danger" type="submit">Sign out everywhere</button></form></div><div class="session-list">{{range .ActiveSessions}}<article><div><strong>{{.ClientLabel}}</strong>{{if .Current}}<em>Current session</em>{{end}}<small>Signed in with {{.AuthenticationMethod}} · Last confirmed with {{.ReauthenticationMethod}}</small><small>Last used {{.LastSeenAt.Format "Jan 2, 2006 at 15:04 UTC"}} · Expires {{.ExpiresAt.Format "Jan 2, 2006"}}</small></div><form method="post" action="/app/security/sessions/revoke"><input type="hidden" name="session_id" value="{{.ID}}"><button type="submit">{{if .Current}}Sign out{{else}}Revoke{{end}}</button></form></article>{{else}}<p>No active sessions.</p>{{end}}</div></section>
    {{if .PasskeysConfigured}}<section class="security-card security-passkeys"><div class="security-card-head"><div><p class="eyebrow">PASSKEYS</p><h2>Phishing-resistant sign-in</h2></div></div><p>Passkeys belong to your Infinite Ocean identity and work across every Spyglass Account you can access. Confirm your identity before adding or removing one.</p><div class="passkey-list">{{range .Passkeys}}<article><div><strong>{{.Name}}</strong><small>Added {{.CreatedAt.Format "Jan 2, 2006"}}{{if .LastUsedAt}} · Last used {{.LastUsedAt.Format "Jan 2, 2006"}}{{end}}{{if .BackedUp}} · Synced{{end}}</small></div><button class="passkey-remove" type="button" data-credential-id="{{.ID}}">Remove</button></article>{{else}}<p>No passkeys have been added.</p>{{end}}</div><div class="passkey-enroll"><label>Passkey name<input id="passkey-name" maxlength="80" value="My passkey" autocomplete="off"></label><button id="passkey-register" type="button">Add passkey</button></div></section>{{end}}
    <section class="security-card security-events"><p class="eyebrow">SECURITY HISTORY</p><h2>Recent identity activity</h2><div class="event-list">{{range .SecurityEvents}}<article><i aria-hidden="true"></i><div><strong>{{.Label}}</strong><small>{{.Detail}}</small></div><time>{{.OccurredAt.Format "Jan 2, 2006 at 15:04 UTC"}}</time></article>{{else}}<p>No security events have been recorded.</p>{{end}}</div></section>
  </div>
</main></body></html>
{{end}}

{{define "app"}}
{{template "head" .}}
<div class="app-shell">
  {{template "private-sidebar" .}}
  <main class="workspace">
    {{template "private-topbar" .}}
    <div class="content">
      {{template "alert" .}}
      {{if .DevelopmentToken}}<div class="dev-link"><strong>Development invitation link</strong><a href="/invitations/accept?token={{.DevelopmentToken}}">Open invitation</a></div>{{end}}
      {{if .Selected}}
      <section class="hero-panel"><div><p class="eyebrow">ACCOUNT OVERVIEW</p><h1>Your Account is ready.<br><em>The operating surface comes next.</em></h1><p>Identity, Membership, placement, package access, and the local entitlement snapshot are active for this Account.</p></div><div class="horizon" aria-hidden="true"><i></i><b></b></div></section>
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
        <div class="plan-grid">{{range .BillingPlans}}<article class="plan-card {{if .Current}}current{{end}}"><div><small>{{if .Current}}CURRENT PLAN{{else}}{{.PackageCount}} PACKAGES{{end}}</small><h3>{{.Name}}</h3><p>{{.Description}}</p></div><div class="plan-price"><strong>{{.Price}}</strong><span>/ {{.Interval}}</span></div>{{if .Current}}<button type="button" disabled>Current access</button>{{else if not $.BillingConfigured}}<span class="plan-note">Paid billing is disabled in this development environment.</span>{{else if $.CanStartCheckout}}<form method="post" action="/app/billing/checkout"><input type="hidden" name="account_id" value="{{$.Selected.AccountID}}"><input type="hidden" name="offer_code" value="{{.OfferCode}}"><button type="submit">Choose {{.Name}} →</button></form>{{else if $.CanManageBilling}}{{if $.HasBillingCustomer}}<span class="plan-note">Use the billing portal to change plans.</span>{{else}}<span class="plan-note">Checkout is not available yet.</span>{{end}}{{else}}<span class="plan-note">An Account billing administrator can manage this plan.</span>{{end}}</article>{{end}}</div>
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
  <main class="workspace">
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
        <div class="work-summary" aria-label="Work summary">
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
            <div class="work-list" id="work-list"></div>
            <button class="work-more" id="work-more" type="button" hidden>Load more</button>
          </section>
          <aside class="work-detail panel" id="work-detail" aria-live="polite">
            <div class="work-detail-empty"><p class="eyebrow">WORK DETAIL</p><h2>Select an item</h2><p>Open a queue item to inspect its responsibility, origin, timing, and current state.</p></div>
          </aside>
        </div>
      </section>
      {{if not .WorkReadOnly}}<dialog class="work-dialog" id="work-create-dialog"><form id="work-create-form"><header><div><p class="eyebrow">NEW WORK</p><h2>Create a clear next step</h2></div><button id="work-create-close" type="button" aria-label="Close">×</button></header><label>Title<input name="title" maxlength="240" required placeholder="What needs to happen?"></label><label>Description<textarea name="description" maxlength="20000" rows="5" placeholder="Add the outcome, context, and definition of done."></textarea></label><div class="work-form-grid"><label>Type<select name="kind"><option value="ticket">Ticket</option><option value="todo">To-do</option></select></label><label>Priority<select name="priority"><option value="normal">Normal</option><option value="high">High</option><option value="urgent">Urgent</option><option value="low">Low</option></select></label><label>Responsibility<select name="responsibility"><option value="shared">Shared</option><option value="user">Assign to me</option></select></label></div><p class="work-form-error" id="work-create-error" role="alert" hidden></p><footer><button class="secondary" id="work-create-cancel" type="button">Cancel</button><button class="primary" type="submit">Create work</button></footer></form></dialog>{{end}}
      {{end}}
    </div>
  </main>
</div></body></html>
{{end}}
`
