// Package operationsstaff provides the deliberately privileged, offline
// governance command used to grant and revoke Operations Console roles.
package operationsstaff

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	DatabaseURL string
	Environment string
	Arguments   []string
	Output      io.Writer
}

var staffRoles = map[operations.StaffRole]bool{
	operations.RoleAdministrator: true, operations.RoleSupport: true, operations.RoleBilling: true,
	operations.RoleAnalytics: true, operations.RolePrivacy: true, operations.RoleAffiliate: true,
}

func Run(ctx context.Context, config Config) error {
	if strings.TrimSpace(config.DatabaseURL) == "" || strings.TrimSpace(config.Environment) == "" || config.Output == nil || len(config.Arguments) == 0 {
		return errors.New("operations staff database, environment, action and output are required")
	}
	action := config.Arguments[0]
	flags := flag.NewFlagSet("operations-staff "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "exact verified User email")
	roleValue := flags.String("role", "", "staff role")
	actor := flags.String("actor", "", "person authorizing the change")
	reason := flags.String("reason", "", "plain-language change reason")
	if err := flags.Parse(config.Arguments[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("operations staff arguments are invalid")
	}
	if action != "assign" && action != "revoke" && action != "show" {
		return errors.New("operations staff action must be assign, revoke, or show")
	}
	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	if normalizedEmail == "" || !strings.Contains(normalizedEmail, "@") || strings.ContainsAny(normalizedEmail, "\x00\r\n\t ") {
		return errors.New("an exact normalized User email is required")
	}
	role := operations.StaffRole(strings.TrimSpace(*roleValue))
	if action != "show" && (!staffRoles[role] || len(strings.TrimSpace(*actor)) < 3 || len(strings.TrimSpace(*reason)) < 8) {
		return errors.New("a valid role, actor, and reason of at least eight characters are required")
	}

	pool, err := pgxpool.New(ctx, config.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	var userID ids.UserID
	var displayName string
	err = pool.QueryRow(ctx, `SELECT id,display_name FROM users WHERE primary_email=$1 AND state='active'`, normalizedEmail).Scan(&userID, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("active User was not found for that exact email")
	}
	if err != nil {
		return fmt.Errorf("resolve operations staff User: %w", err)
	}
	if action == "assign" {
		var passkeyCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM passkey_credentials WHERE user_id=$1`, userID).Scan(&passkeyCount); err != nil {
			return fmt.Errorf("verify operations staff passkey: %w", err)
		}
		if passkeyCount == 0 {
			return errors.New("the User must register a passkey before receiving an operations role")
		}
		generator := ids.RandomGenerator{}
		_, err = pool.Exec(ctx, `SELECT spyglass_operations_assign_staff_role($1,$2,$3,$4,$5,$6,$7)`,
			generator.New(), generator.New(), userID, role, strings.TrimSpace(*actor), strings.TrimSpace(*reason), strings.TrimSpace(config.Environment))
	} else if action == "revoke" {
		_, err = pool.Exec(ctx, `SELECT spyglass_operations_revoke_staff_role($1,$2,$3,$4,$5,$6)`,
			ids.RandomGenerator{}.New(), userID, role, strings.TrimSpace(*actor), strings.TrimSpace(*reason), strings.TrimSpace(config.Environment))
	}
	if err != nil {
		return fmt.Errorf("%s operations staff role: %w", action, err)
	}
	var state string
	var roles []string
	err = pool.QueryRow(ctx, `SELECT staff_state,roles FROM spyglass_operations_current_staff($1)`, userID).Scan(&state, &roles)
	if errors.Is(err, pgx.ErrNoRows) && action == "show" {
		return errors.New("the User is not operations staff")
	}
	if err != nil {
		return fmt.Errorf("load operations staff result: %w", err)
	}
	return json.NewEncoder(config.Output).Encode(map[string]any{
		"user_id": userID, "display_name": displayName, "email": normalizedEmail, "state": state, "roles": roles,
	})
}
