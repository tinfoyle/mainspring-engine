package operationsapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
)

func (server *Server) traffic(w http.ResponseWriter, request *http.Request) {
	_, staff, ok := server.authorizedStaff(w, request, operations.RoleAdministrator)
	if !ok {
		return
	}
	var input struct {
		From   time.Time `json:"from"`
		To     time.Time `json:"to"`
		Ticket string    `json:"ticket"`
		Reason string    `json:"reason"`
	}
	if !server.decode(w, request, &input) {
		return
	}
	if server.operators.Traffic == nil {
		writeProblem(w, http.StatusServiceUnavailable, "traffic_unavailable", "Traffic logs are not configured for this environment.")
		return
	}
	report, err := server.operators.Traffic.Report(request.Context(), staff.UserID, trafficreport.Query{From: input.From, To: input.To}, operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason})
	if errors.Is(err, trafficreport.ErrInvalidQuery) {
		writeProblem(w, http.StatusBadRequest, "invalid_traffic_query", "Choose a period within the last seven days and provide a ticket and reason.")
		return
	}
	if errors.Is(err, trafficreport.ErrUnavailable) {
		writeProblem(w, http.StatusServiceUnavailable, "traffic_unavailable", "Traffic logs could not be read. Check the log mount and collection status.")
		return
	}
	if err != nil {
		server.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
