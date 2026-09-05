package operationsapi

import (
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"net/http"
)

func (s *Server) directory(w http.ResponseWriter, request *http.Request) {
	if !s.authorizeMutation(w, request) {
		return
	}
	_, staff, ok := s.authenticate(w, request)
	if !ok {
		return
	}
	if !staff.HasRole(operations.RoleAdministrator) {
		s.writeOperationsError(w, operations.ErrStaffUnauthorized)
		return
	}
	var input struct {
		Kind     string `json:"kind"`
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
		Ticket   string `json:"ticket"`
		Reason   string `json:"reason"`
	}
	if !s.decode(w, request, &input) {
		return
	}
	result, err := s.console.Directory(request.Context(), staff.UserID, operations.DirectoryQuery{
		Kind: input.Kind, Page: input.Page, PageSize: input.PageSize, Audit: operations.AuditReason{Ticket: input.Ticket, Reason: input.Reason}})
	if err != nil {
		s.writeOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
