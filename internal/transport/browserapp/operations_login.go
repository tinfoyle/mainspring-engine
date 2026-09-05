package browserapp

import (
	"html/template"
	"net/http"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
)

func WithOperationsLogin(origin string, cipher *passkeys.Cipher) Option {
	return func(s *Server) { s.operationsOrigin = origin; s.operationsCipher = cipher }
}
func (s *Server) beginOperationsGoogle(w http.ResponseWriter, r *http.Request) {
	if s.googleProvider == nil || s.operationsCipher == nil || s.operationsOrigin == "" {
		http.NotFound(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	if !operationsauth.ValidToken(state) {
		http.Error(w, "Start sign-in from the admin console.", 400)
		return
	}
	// Always run Google's authorization flow, even when a customer cookie exists.
	s.beginGoogleFlow(w, r, googleFlow{Mode: "operations", OperationsState: state})
}

var operationsHandoff = template.Must(template.New("operations-handoff").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Continue to Spyglass Admin</title></head><body><main><h1>Google sign-in complete</h1><form method="post" action="{{.Action}}"><input type="hidden" name="ticket" value="{{.Ticket}}"><button type="submit">Continue to admin</button></form></main><script nonce="{{.Nonce}}">document.querySelector('form').submit();</script></body></html>`))

func (s *Server) completeOperationsGoogle(w http.ResponseWriter, r *http.Request, flow googleFlow, assertion oidcauth.Assertion) {
	if s.operationsCipher == nil || s.operationsOrigin == "" {
		http.Error(w, "Admin sign-in is unavailable.", 503)
		return
	}
	identifier, err := oidcauth.Identifier(assertion)
	if err != nil || assertion.Issuer != "https://accounts.google.com" {
		http.Error(w, "Google sign-in was not accepted.", 401)
		return
	}
	ticket, err := operationsauth.SealTicket(s.operationsCipher, operationsauth.Ticket{Identifier: identifier, State: flow.OperationsState, Audience: s.operationsOrigin, ExpiresAt: time.Now().UTC().Add(time.Minute)})
	if err != nil {
		http.Error(w, "Admin sign-in is unavailable.", 503)
		return
	}
	nonce, err := googleRandom()
	if err != nil {
		http.Error(w, "Admin sign-in is unavailable.", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action "+s.operationsOrigin+"; frame-ancestors 'none'; base-uri 'none'")
	_ = operationsHandoff.Execute(w, map[string]string{"Action": s.operationsOrigin + "/api/operations/v1/auth/google/complete", "Ticket": ticket, "Nonce": nonce})
}
