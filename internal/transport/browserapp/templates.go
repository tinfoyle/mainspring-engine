package browserapp

const pageTemplates = `
{{define "head"}}
<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · Infinite Ocean: Spyglass</title><meta name="description" content="Infinite Ocean: Spyglass business operating system">
<link rel="stylesheet" href="/assets/spyglass.css"></head><body>
{{end}}

{{define "brand"}}
<a class="brand" href="https://infiniteocean.net" aria-label="Infinite Ocean home"><i aria-hidden="true"><b></b></i><span><strong>INFINITE OCEAN</strong><small>SPYGLASS</small></span></a>
{{end}}

{{define "alert"}}
{{if .Error}}<div class="alert error" role="alert">{{.Error}}</div>{{end}}
{{if .Notice}}<div class="alert success" role="status">{{.Notice}}</div>{{end}}
{{end}}

{{define "login"}}
{{template "head" .}}<main class="auth"><section class="auth-story">{{template "brand" .}}<div><p class="eyebrow">WELCOME BACK</p><h1>Find the signal.<br><em>Move the business.</em></h1><p>Sign in once, then choose the Spyglass Account where you want to work.</p></div><footer>One identity · Explicit Account access · Package-aware</footer></section><section class="auth-panel"><form method="post" action="/login"><p class="eyebrow">SECURE ACCESS</p><h2>Sign in to Spyglass</h2>{{template "alert" .}}<input type="hidden" name="return_to" value="{{.ReturnTo}}"><label>Email address<input type="email" name="email" value="{{.Email}}" autocomplete="email" required autofocus></label><label>Password<input type="password" name="password" autocomplete="current-password" required></label><button type="submit">Sign in <span>→</span></button><p class="form-note">New to Infinite Ocean? <a href="/signup">Create a free Account</a>.</p></form></section></main></body></html>
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

{{define "app"}}
{{template "head" .}}
<div class="app-shell">
  <aside class="sidebar">
    {{template "brand" .}}
    <form class="account-switch" method="post" action="/app/account">
      <label>ACTIVE ACCOUNT<select name="account_id">{{range .Choices}}<option value="{{.AccountID}}" {{if $.Selected}}{{if eq .AccountID $.Selected.AccountID}}selected{{end}}{{end}}>{{.DisplayName}}</option>{{end}}</select></label>
      <button type="submit">Switch Account</button>
    </form>
    <nav><p>OPERATE</p><a class="active" href="/app"><i>⌂</i>Overview</a><a href="#work"><i>✓</i>Work</a><a href="#agents"><i>◌</i>Agents</a><a href="#knowledge"><i>◇</i>Knowledge</a><p>BUSINESS</p><a href="#finance"><i>≋</i>Finance</a><a href="#marketing"><i>↗</i>Marketing</a><a href="#settings"><i>⚙</i>Account</a></nav>
    <form method="post" action="/logout"><button class="logout">Sign out</button></form>
  </aside>
  <main class="workspace">
    <header class="topbar"><div><strong>{{if .Selected}}{{.Selected.DisplayName}}{{else}}No Account selected{{end}}</strong><small>Infinite Ocean: Spyglass</small></div><span class="live"><i></i>Account services ready</span></header>
    <div class="content">
      {{template "alert" .}}
      {{if .DevelopmentToken}}<div class="dev-link"><strong>Development invitation link</strong><a href="/invitations/accept?token={{.DevelopmentToken}}">Open invitation</a></div>{{end}}
      {{if .Selected}}
      <section class="hero-panel"><div><p class="eyebrow">ACCOUNT OVERVIEW</p><h1>Your Account is ready.<br><em>The operating surface comes next.</em></h1><p>Identity, Membership, placement, package access, and the local entitlement snapshot are active for this Account.</p></div><div class="horizon" aria-hidden="true"><i></i><b></b></div></section>
      <section class="metrics">
        <article><small>ACCOUNT TYPE</small><strong>{{.Selected.AccountType}}</strong><span class="green">No billing required</span></article>
        <article><small>PACKAGE ACCESS</small><strong>{{len .PackageModes}}</strong><span>Effective packages</span></article>
        <article><small>YOUR ROLE</small><strong>{{.Selected.Role}}</strong><span>Active Membership</span></article>
        <article><small>ACCESS VERSION</small><strong>{{.Selected.Entitlements.Version}}</strong><span>Local snapshot</span></article>
      </section>
      <div class="dashboard-grid">
        <section class="panel" id="work"><header><div><p class="eyebrow">WORK</p><h2>What’s moving</h2></div><span>Module boundary reserved</span></header><div class="work-empty"><strong>No operational work has been created.</strong><p>The production Work module will populate this view through Account-scoped queries; this shell does not invent customer activity.</p></div></section>
        <aside class="mia"><header><b>M</b><div><small>YOUR OPERATING PARTNER</small><strong>Mia</strong></div><i></i></header><p>No items are waiting for your attention. Mia will work only through enabled packages and explicitly granted capabilities.</p><button type="button" disabled>Nothing waiting</button><footer><i></i>Bound to this Account’s permissions</footer></aside>
      </div>
      <section class="packages"><header><div><p class="eyebrow">FEATURE PACKAGES</p><h2>Your operating surface</h2></div><span>{{.Selected.AccountType}} Account</span></header><div>{{range .Catalog.Packages}}<article id="{{.Code}}"><b>·</b><div><strong>{{.Name}}</strong><small>{{.Description}}</small></div><em>{{with index $.PackageModes .Code}}{{.}}{{else}}locked{{end}}</em></article>{{end}}</div></section>
      {{if .CanInvite}}<section class="team panel" id="settings"><header><div><p class="eyebrow">ACCOUNT</p><h2>Invite a teammate</h2></div><span>Your role: {{.Selected.Role}}</span></header><form method="post" action="/app/invitations"><input type="hidden" name="account_id" value="{{.Selected.AccountID}}"><label>Email<input type="email" name="email" required placeholder="teammate@company.com"></label><label>Role<select name="role"><option value="member">Member</option><option value="viewer">Viewer</option><option value="administrator">Administrator</option><option value="billing_admin">Billing admin</option></select></label><button type="submit">Send invitation</button></form></section>{{end}}
      {{else}}<section class="empty"><h1>No Spyglass Accounts yet.</h1><p>Create an Account or accept an invitation to begin.</p><a href="/signup">Create Account</a></section>{{end}}
    </div>
  </main>
</div></body></html>
{{end}}
`
