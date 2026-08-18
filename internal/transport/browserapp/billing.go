package browserapp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/commercialaccess"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (s *Server) startCheckout(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.billingForm(w, r)
	if !ok {
		return
	}
	session, err := s.commercial.Checkout(r.Context(), commercialaccess.CheckoutCommand{ActorUserID: authenticated.Session.UserID, AccountID: accountID, OfferCode: r.FormValue("offer_code"), RequestID: ids.RandomGenerator{}.New()})
	if err != nil {
		http.Redirect(w, r, "/app?status=billing_failed#billing", http.StatusSeeOther)
		return
	}
	s.redirectHosted(w, r, session.URL)
}

func (s *Server) openBillingPortal(w http.ResponseWriter, r *http.Request) {
	authenticated, accountID, ok := s.billingForm(w, r)
	if !ok {
		return
	}
	session, err := s.commercial.Portal(r.Context(), commercialaccess.PortalCommand{ActorUserID: authenticated.Session.UserID, AccountID: accountID, RequestID: ids.RandomGenerator{}.New()})
	if err != nil {
		http.Redirect(w, r, "/app?status=billing_failed#billing", http.StatusSeeOther)
		return
	}
	s.redirectHosted(w, r, session.URL)
}

func (s *Server) billingForm(w http.ResponseWriter, r *http.Request) (sessions.Authenticated, ids.AccountID, bool) {
	authenticated, ok := s.requireSession(w, r)
	if !ok {
		return sessions.Authenticated{}, "", false
	}
	if s.commercial == nil {
		http.Redirect(w, r, "/app?status=billing_unavailable#billing", http.StatusSeeOther)
		return sessions.Authenticated{}, "", false
	}
	if !s.validOrigin(r, false) {
		http.Error(w, "Request origin was not accepted.", http.StatusForbidden)
		return sessions.Authenticated{}, "", false
	}
	if err := s.parseForm(w, r); err != nil {
		http.Error(w, "Invalid request.", http.StatusBadRequest)
		return sessions.Authenticated{}, "", false
	}
	raw := r.FormValue("account_id")
	if ids.Validate(raw) != nil {
		http.Error(w, "Invalid Account.", http.StatusBadRequest)
		return sessions.Authenticated{}, "", false
	}
	return authenticated, ids.AccountID(raw), true
}

func (s *Server) redirectHosted(w http.ResponseWriter, r *http.Request, raw string) {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		http.Redirect(w, r, "/app?status=billing_failed#billing", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func billingView(publication catalog.PublishedCatalog, accountType accounts.AccountType, status commercialaccess.Status, now time.Time) ([]billingPlan, string, string, string) {
	currentOffer, state, period, synced := "free-v1", "Free access", "No billing required", "Local entitlement snapshot"
	if len(status.Subscriptions) > 0 {
		current, managed := status.Subscriptions[0], false
		for _, candidate := range status.Subscriptions {
			if candidate.State != "canceled" && candidate.State != "incomplete_expired" {
				current, managed = candidate, true
				break
			}
		}
		state = strings.ReplaceAll(current.State, "_", " ")
		if managed {
			currentOffer = current.OfferCode
		}
		if current.CurrentPeriodEnd != nil {
			period = "Current period through " + current.CurrentPeriodEnd.UTC().Format("Jan 2, 2006")
		}
		if !current.LastSyncedAt.IsZero() {
			synced = "Synced " + current.LastSyncedAt.UTC().Format("Jan 2, 15:04 UTC")
		}
	} else if accountType == accounts.AccountPaid {
		state = "Paid access"
	}
	plans := make([]billingPlan, 0, len(publication.Offers))
	for _, offer := range publication.Offers {
		if !offer.Published || offer.AmountMinor <= 0 || offer.EffectiveFrom.After(now) {
			continue
		}
		plan, ok := publication.Plan(offer.PlanCode)
		if !ok {
			continue
		}
		price := fmt.Sprintf("$%d", offer.AmountMinor/100)
		if cents := offer.AmountMinor % 100; cents != 0 {
			price = fmt.Sprintf("$%d.%02d", offer.AmountMinor/100, cents)
		}
		plans = append(plans, billingPlan{OfferCode: offer.Code, Name: plan.Name, Description: plan.Description, Price: price, Interval: offer.BillingInterval, PackageCount: len(plan.Packages), Current: offer.Code == currentOffer})
	}
	return plans, state, period, synced
}
