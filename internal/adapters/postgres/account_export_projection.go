package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var exportSQLIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// AccountExportProjectionTable is an explicit allowlist. Columns not named
// here cannot enter a portability artifact, even when the physical table gains
// a new column. KeyColumns establish the immutable record identity.
type AccountExportProjectionTable struct {
	Tx             pgx.Tx
	Schema         string
	Table          string
	AccountColumn  string
	KeyColumns     []string
	Columns        []string
	OmittedColumns []string
}

type AccountExportSectionSource struct {
	descriptor accountexport.Descriptor
	accountID  ids.AccountID
	tables     []AccountExportProjectionTable
}

func NewAccountExportSectionSource(descriptor accountexport.Descriptor, accountID ids.AccountID, tables []AccountExportProjectionTable) (*AccountExportSectionSource, error) {
	tables = append([]AccountExportProjectionTable(nil), tables...)
	if ids.Validate(string(accountID)) != nil || descriptor.Code == "" || descriptor.SchemaVersion == 0 || len(descriptor.Stores) == 0 || len(tables) == 0 {
		return nil, accountexport.ErrInvalid
	}
	for index := range tables {
		table := &tables[index]
		table.KeyColumns = append([]string(nil), table.KeyColumns...)
		table.Columns = append([]string(nil), table.Columns...)
		table.OmittedColumns = append([]string(nil), table.OmittedColumns...)
		if !validProjectionTable(*table) {
			return nil, accountexport.ErrInvalid
		}
	}
	if !slices.IsSortedFunc(tables, func(left, right AccountExportProjectionTable) int {
		return strings.Compare(left.Schema+"."+left.Table, right.Schema+"."+right.Table)
	}) {
		return nil, accountexport.ErrInvalid
	}
	for index := 1; index < len(tables); index++ {
		if tables[index-1].Schema == tables[index].Schema && tables[index-1].Table == tables[index].Table {
			return nil, accountexport.ErrInvalid
		}
	}
	descriptor.Stores = append([]string(nil), descriptor.Stores...)
	return &AccountExportSectionSource{descriptor: descriptor, accountID: accountID, tables: tables}, nil
}

func (source *AccountExportSectionSource) Descriptor() accountexport.Descriptor {
	value := source.descriptor
	value.Stores = append([]string(nil), value.Stores...)
	return value
}

func (source *AccountExportSectionSource) Open(_ context.Context, request accountexport.BuildRequest) (accountexport.RecordCursor, error) {
	if request.AccountID != source.accountID {
		return nil, accountexport.ErrInvalid
	}
	return &accountExportProjectionCursor{accountID: source.accountID, tables: source.tables}, nil
}

type accountExportProjectionCursor struct {
	accountID ids.AccountID
	tables    []AccountExportProjectionTable
	index     int
	rows      pgx.Rows
	closed    bool
}

func (cursor *accountExportProjectionCursor) Next(ctx context.Context) (json.RawMessage, bool, error) {
	if cursor == nil || cursor.closed {
		return nil, false, accountexport.ErrInvalid
	}
	for {
		if cursor.rows == nil {
			if cursor.index == len(cursor.tables) {
				return nil, false, nil
			}
			table := cursor.tables[cursor.index]
			rows, err := table.Tx.Query(ctx, projectionQuery(table), cursor.accountID)
			if err != nil {
				return nil, false, errors.Join(accountexport.ErrUnavailable, err)
			}
			cursor.rows = rows
		}
		if cursor.rows.Next() {
			table := cursor.tables[cursor.index]
			var keySuffix string
			var payload []byte
			if err := cursor.rows.Scan(&keySuffix, &payload); err != nil {
				return nil, false, errors.Join(accountexport.ErrUnavailable, err)
			}
			record, err := canonicalProjectionRecord(table.Schema+"."+table.Table+"/"+keySuffix, payload)
			if err != nil {
				return nil, false, err
			}
			return record, true, nil
		}
		err := cursor.rows.Err()
		cursor.rows.Close()
		cursor.rows = nil
		cursor.index++
		if err != nil {
			return nil, false, errors.Join(accountexport.ErrUnavailable, err)
		}
	}
}

func (cursor *accountExportProjectionCursor) Close() error {
	if cursor == nil || cursor.closed {
		return nil
	}
	cursor.closed = true
	if cursor.rows != nil {
		cursor.rows.Close()
		if err := cursor.rows.Err(); err != nil {
			return errors.Join(accountexport.ErrUnavailable, err)
		}
	}
	return nil
}

func validProjectionTable(table AccountExportProjectionTable) bool {
	if table.Tx == nil || !validExportIdentifier(table.Schema) || !validExportIdentifier(table.Table) || !validExportIdentifier(table.AccountColumn) || len(table.KeyColumns) == 0 || len(table.Columns) == 0 {
		return false
	}
	seen := make(map[string]bool, len(table.Columns)+len(table.OmittedColumns))
	for _, column := range table.Columns {
		if !validExportIdentifier(column) || column == "key" || seen[column] {
			return false
		}
		seen[column] = true
	}
	for _, column := range table.OmittedColumns {
		if !validExportIdentifier(column) || column == "key" || seen[column] {
			return false
		}
		seen[column] = true
	}
	for _, column := range table.KeyColumns {
		if !validExportIdentifier(column) || !seen[column] {
			return false
		}
	}
	return true
}

func validExportIdentifier(value string) bool { return exportSQLIdentifier.MatchString(value) }

func sortAccountExportProjectionTables(sections map[string][]AccountExportProjectionTable) {
	for section := range sections {
		slices.SortFunc(sections[section], func(left, right AccountExportProjectionTable) int {
			return strings.Compare(left.Schema+"."+left.Table, right.Schema+"."+right.Table)
		})
	}
}

func projectionQuery(table AccountExportProjectionTable) string {
	qualified := pgx.Identifier{table.Schema, table.Table}.Sanitize()
	keys := make([]string, len(table.KeyColumns))
	for index, column := range table.KeyColumns {
		keys[index] = pgx.Identifier{column}.Sanitize()
	}
	fields := make([]string, 0, len(table.Columns)*2)
	for _, column := range table.Columns {
		fields = append(fields, "'"+column+"'", pgx.Identifier{column}.Sanitize())
	}
	accountColumn := pgx.Identifier{table.AccountColumn}.Sanitize()
	keyExpression := "encode(convert_to(jsonb_build_array(" + strings.Join(keys, ",") + ")::text,'UTF8'),'hex')"
	return "SELECT " + keyExpression + ",jsonb_build_object(" + strings.Join(fields, ",") + ") FROM " + qualified +
		" WHERE " + accountColumn + "=$1 ORDER BY (" + keyExpression + ") COLLATE \"C\""
}

func canonicalProjectionRecord(key string, payload []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil || value["key"] != nil {
		return nil, accountexport.ErrInvalid
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return nil, accountexport.ErrInvalid
	}
	value["key"] = key
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical projection: %v", accountexport.ErrInvalid, err)
	}
	return encoded, nil
}

var _ accountexport.SectionSource = (*AccountExportSectionSource)(nil)
