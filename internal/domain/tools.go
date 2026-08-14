package domain

import "time"

type Capability string

const (
	CapabilityWebSearch        Capability = "web.search"
	CapabilityWebRead          Capability = "web.read"
	CapabilityDocumentsRead    Capability = "documents.read"
	CapabilityDocumentsWrite   Capability = "documents.write"
	CapabilityDocumentsComment Capability = "documents.comment"
	CapabilityEmailDraft       Capability = "email.draft"
	CapabilityEmailRead        Capability = "email.read"
	CapabilityEmailSend        Capability = "email.send"
	CapabilityTicketRead       Capability = "tickets.read"
	CapabilityTicketCreate     Capability = "tickets.create"
	CapabilityScheduleRead     Capability = "schedules.read"
	CapabilitySchedulePropose  Capability = "schedules.propose"
	CapabilityScheduleModify   Capability = "schedules.modify"
	CapabilityInvoicePrepare   Capability = "invoice.prepare"
	CapabilityInvoiceIssue     Capability = "invoice.issue"
	CapabilityPaymentPropose   Capability = "payment.propose"
	CapabilityPaymentExecute   Capability = "payment.execute"
	CapabilityFinanceRead      Capability = "finance.read"
	CapabilityFinanceManage    Capability = "finance.manage"
)

type ToolGrant struct {
	Capability Capability
	Conditions map[string]string
}

type InvocationContext struct {
	TenantID     TenantID
	BoardroomID  BoardroomID
	RunID        RunID
	PersonaID    PersonaID
	InvocationID InvocationID
	ActorType    string
	ActorID      string
	Grants       []ToolGrant
	ExpiresAt    time.Time
}
