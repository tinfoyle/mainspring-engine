// Package operationsconsole coordinates staff-only, least-authority access to
// aggregate analytics, administrator directories and exact support projections.
package operationsconsole

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,79}$`)

type Repository interface {
	Staff(context.Context, ids.UserID) (operations.Staff, error)
	Directory(context.Context, operations.Staff, operations.DirectoryQuery, ids.OperationsAuditEventID, string) (operations.DirectoryPage, error)
	Lookup(context.Context, operations.Staff, operations.LookupQuery, ids.OperationsAuditEventID, string, time.Time) ([]operations.LookupResult, error)
	CreateGrant(context.Context, operations.SupportGrant, ids.OperationsAuditEventID, string) (operations.SupportGrant, error)
	Grant(context.Context, ids.OperationsSupportGrantID, ids.UserID) (operations.SupportGrant, error)
	ViewAccount(context.Context, operations.Staff, operations.SupportGrant, operations.AuditReason, ids.OperationsAuditEventID, string, time.Time) (operations.AccountView, error)
	RevokeGrant(context.Context, operations.Staff, ids.OperationsSupportGrantID, uint64, operations.AuditReason, ids.OperationsAuditEventID, string, time.Time) (operations.SupportGrant, error)
	CustomerHistory(context.Context, ids.UserID, ids.AccountID, int) ([]operations.AccessEvent, error)
	Analytics(context.Context, operations.Staff, analyticsreport.Query, operations.AuditReason, ids.OperationsAuditEventID, string, time.Time) (analyticsreport.Report, error)
	RecordAuthentication(context.Context, operations.Staff, ids.SessionID, ids.OperationsAuditEventID, string, time.Time) error
	RecordLogout(context.Context, operations.Staff, ids.SessionID, ids.OperationsAuditEventID, string, time.Time) error
}

type Service struct {
	repository  Repository
	ids         ids.Generator
	clock       sessions.Clock
	environment string
}

func New(repository Repository, generator ids.Generator, clock sessions.Clock, environment string) (*Service, error) {
	environment = strings.TrimSpace(environment)
	if repository == nil || generator == nil || clock == nil || !environmentPattern.MatchString(environment) {
		return nil, operations.ErrInvalidInput
	}
	return &Service{repository: repository, ids: generator, clock: clock, environment: environment}, nil
}

func (service *Service) Staff(ctx context.Context, userID ids.UserID) (operations.Staff, error) {
	if ids.Validate(string(userID)) != nil {
		return operations.Staff{}, operations.ErrStaffUnauthorized
	}
	staff, err := service.repository.Staff(ctx, userID)
	if err != nil || staff.State != operations.StaffActive || len(staff.Roles) == 0 {
		return operations.Staff{}, operations.ErrStaffUnauthorized
	}
	return staff, nil
}

func (service *Service) RecordAuthentication(ctx context.Context, userID ids.UserID, sessionID ids.SessionID) (operations.Staff, error) {
	staff, err := service.Staff(ctx, userID)
	if err != nil {
		return operations.Staff{}, err
	}
	if ids.Validate(string(sessionID)) != nil {
		return operations.Staff{}, operations.ErrInvalidInput
	}
	if err := service.repository.RecordAuthentication(ctx, staff, sessionID, service.eventID(), service.environment, service.now()); err != nil {
		return operations.Staff{}, err
	}
	return staff, nil
}

func (service *Service) RecordLogout(ctx context.Context, userID ids.UserID, sessionID ids.SessionID) error {
	staff, err := service.Staff(ctx, userID)
	if err != nil {
		return err
	}
	if ids.Validate(string(sessionID)) != nil {
		return operations.ErrInvalidInput
	}
	return service.repository.RecordLogout(ctx, staff, sessionID, service.eventID(), service.environment, service.now())
}

func (service *Service) Lookup(ctx context.Context, actor ids.UserID, query operations.LookupQuery) ([]operations.LookupResult, error) {
	staff, err := service.Staff(ctx, actor)
	if err != nil {
		return nil, err
	}
	if !staff.HasRole(operations.RoleSupport, operations.RoleBilling, operations.RolePrivacy, operations.RoleAffiliate) {
		return nil, operations.ErrStaffUnauthorized
	}
	query.Value = strings.TrimSpace(query.Value)
	query.Audit.Ticket = strings.TrimSpace(query.Audit.Ticket)
	query.Audit.Reason = strings.TrimSpace(query.Audit.Reason)
	if err := query.Validate(); err != nil {
		return nil, err
	}
	values, err := service.repository.Lookup(ctx, staff, query, service.eventID(), service.environment, service.now())
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = []operations.LookupResult{}
	}
	return values, nil
}

type CreateGrantCommand struct {
	ActorUserID  ids.UserID
	TargetUserID ids.UserID
	AccountID    ids.AccountID
	Lifetime     time.Duration
	Audit        operations.AuditReason
}

func (service *Service) CreateGrant(ctx context.Context, command CreateGrantCommand) (operations.SupportGrant, error) {
	staff, err := service.Staff(ctx, command.ActorUserID)
	if err != nil {
		return operations.SupportGrant{}, err
	}
	if !staff.HasRole(operations.RoleSupport) || command.ActorUserID == command.TargetUserID ||
		ids.Validate(string(command.TargetUserID)) != nil || ids.Validate(string(command.AccountID)) != nil {
		return operations.SupportGrant{}, operations.ErrStaffUnauthorized
	}
	command.Audit.Ticket = strings.TrimSpace(command.Audit.Ticket)
	command.Audit.Reason = strings.TrimSpace(command.Audit.Reason)
	if err := command.Audit.Validate(); err != nil {
		return operations.SupportGrant{}, err
	}
	if command.Lifetime == 0 {
		command.Lifetime = operations.DefaultGrantLifetime
	}
	if command.Lifetime < operations.MinimumGrantLifetime || command.Lifetime > operations.MaximumGrantLifetime {
		return operations.SupportGrant{}, operations.ErrInvalidInput
	}
	now := service.now()
	grant := operations.SupportGrant{
		ID: ids.OperationsSupportGrantID(service.ids.New()), StaffUserID: staff.UserID,
		TargetUserID: command.TargetUserID, AccountID: command.AccountID, State: operations.GrantActive,
		Ticket: command.Audit.Ticket, Reason: command.Audit.Reason, CreatedAt: now,
		ExpiresAt: now.Add(command.Lifetime), Version: 1,
	}
	if ids.Validate(string(grant.ID)) != nil {
		return operations.SupportGrant{}, operations.ErrInvalidInput
	}
	created, err := service.repository.CreateGrant(ctx, grant, service.eventID(), service.environment)
	if err != nil {
		return operations.SupportGrant{}, err
	}
	return created, nil
}

func (service *Service) ViewAccount(ctx context.Context, actor ids.UserID, grantID ids.OperationsSupportGrantID, audit operations.AuditReason) (operations.AccountView, error) {
	staff, err := service.Staff(ctx, actor)
	if err != nil {
		return operations.AccountView{}, err
	}
	if !staff.HasRole(operations.RoleSupport) || ids.Validate(string(grantID)) != nil {
		return operations.AccountView{}, operations.ErrStaffUnauthorized
	}
	audit.Ticket, audit.Reason = strings.TrimSpace(audit.Ticket), strings.TrimSpace(audit.Reason)
	if err := audit.Validate(); err != nil {
		return operations.AccountView{}, err
	}
	grant, err := service.repository.Grant(ctx, grantID, staff.UserID)
	if err != nil || !grant.AvailableTo(staff, service.now()) {
		return operations.AccountView{}, operations.ErrGrantDenied
	}
	return service.repository.ViewAccount(ctx, staff, grant, audit, service.eventID(), service.environment, service.now())
}

func (service *Service) RevokeGrant(ctx context.Context, actor ids.UserID, grantID ids.OperationsSupportGrantID, expectedVersion uint64, audit operations.AuditReason) (operations.SupportGrant, error) {
	staff, err := service.Staff(ctx, actor)
	if err != nil {
		return operations.SupportGrant{}, err
	}
	if !staff.HasRole(operations.RoleSupport) || ids.Validate(string(grantID)) != nil || expectedVersion == 0 {
		return operations.SupportGrant{}, operations.ErrStaffUnauthorized
	}
	audit.Ticket, audit.Reason = strings.TrimSpace(audit.Ticket), strings.TrimSpace(audit.Reason)
	if err := audit.Validate(); err != nil {
		return operations.SupportGrant{}, err
	}
	return service.repository.RevokeGrant(ctx, staff, grantID, expectedVersion, audit, service.eventID(), service.environment, service.now())
}

func (service *Service) Analytics(ctx context.Context, actor ids.UserID, query analyticsreport.Query, audit operations.AuditReason) (analyticsreport.Report, error) {
	staff, err := service.Staff(ctx, actor)
	if err != nil {
		return analyticsreport.Report{}, err
	}
	if !staff.HasRole(operations.RoleAnalytics) {
		return analyticsreport.Report{}, operations.ErrStaffUnauthorized
	}
	audit.Ticket, audit.Reason = strings.TrimSpace(audit.Ticket), strings.TrimSpace(audit.Reason)
	if err := audit.Validate(); err != nil {
		return analyticsreport.Report{}, err
	}
	if err := analyticsreport.Validate(query); err != nil {
		return analyticsreport.Report{}, err
	}
	return service.repository.Analytics(ctx, staff, query, audit, service.eventID(), service.environment, service.now())
}

func (service *Service) CustomerHistory(ctx context.Context, userID ids.UserID, accountID ids.AccountID, limit int) ([]operations.AccessEvent, error) {
	if ids.Validate(string(userID)) != nil || ids.Validate(string(accountID)) != nil || limit < 1 || limit > 100 {
		return nil, operations.ErrInvalidInput
	}
	values, err := service.repository.CustomerHistory(ctx, userID, accountID, limit)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = []operations.AccessEvent{}
	}
	return values, nil
}

func (service *Service) eventID() ids.OperationsAuditEventID {
	return ids.OperationsAuditEventID(service.ids.New())
}

func (service *Service) now() time.Time { return service.clock.Now().UTC() }

func IsDenied(err error) bool {
	return errors.Is(err, operations.ErrStaffUnauthorized) || errors.Is(err, operations.ErrGrantDenied)
}
