// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type snapshotColumn struct {
	Type       string
	NotNull    int
	Default    sql.NullString
	PrimaryKey int
	Hidden     int
}

type snapshotTable struct {
	Columns              map[string]snapshotColumn
	Indexes              []string
	WithoutRowID, Strict int
	Definition           []schemaToken
}

// The reference is built by the application's migrations in memory, so a
// verifier cannot drift from the schema that restored writes actually require.
// Historical columns, constraints and indexes are reconstructed explicitly by
// downgradeSnapshotSchemaReference before validating an older snapshot.
func newSnapshotSchemaReference(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	reference := &Store{db: db, path: ":memory:"}
	if err := reference.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func readSnapshotTable(ctx context.Context, db *sql.DB, table string) (snapshotTable, error) {
	result := snapshotTable{Columns: make(map[string]snapshotColumn)}
	var tableType, definition string
	if err := db.QueryRowContext(ctx, `SELECT type,sql FROM sqlite_schema WHERE name=?`, table).Scan(&tableType, &definition); err != nil || tableType != "table" {
		return result, fmt.Errorf("backup schema is missing table %s", table)
	}
	var err error
	result.Definition, err = schemaTokens(definition)
	if err != nil {
		return result, err
	}
	if err := db.QueryRowContext(ctx, `SELECT wr,strict FROM pragma_table_list WHERE schema='main' AND name=?`, table).Scan(&result.WithoutRowID, &result.Strict); err != nil {
		return result, err
	}
	rows, err := db.QueryContext(ctx, `SELECT name,type,"notnull",dflt_value,pk,hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var name string
		var column snapshotColumn
		if err := rows.Scan(&name, &column.Type, &column.NotNull, &column.Default, &column.PrimaryKey, &column.Hidden); err != nil {
			rows.Close()
			return result, err
		}
		column.Type = strings.ToUpper(strings.TrimSpace(column.Type))
		result.Columns[name] = column
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	type indexEntry struct {
		name            string
		unique, partial int
	}
	var indexes []indexEntry
	rows, err = db.QueryContext(ctx, `SELECT name,"unique",partial FROM pragma_index_list(?)`, table)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var index indexEntry
		if err := rows.Scan(&index.name, &index.unique, &index.partial); err != nil {
			rows.Close()
			return result, err
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for _, index := range indexes {
		// Compare uniqueness, key order, collation and sort direction rather
		// than generated index names. An equivalent renamed index is valid.
		signature := fmt.Sprintf("unique=%d;partial=%d;", index.unique, index.partial)
		rows, err = db.QueryContext(ctx, `SELECT name,"desc",coll FROM pragma_index_xinfo(?) WHERE key=1 ORDER BY seqno`, index.name)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var column, collation sql.NullString
			var descending int
			if err := rows.Scan(&column, &descending, &collation); err != nil {
				rows.Close()
				return result, err
			}
			if !column.Valid || !collation.Valid {
				rows.Close()
				return result, errors.New("backup contains unsupported expression indexes")
			}
			signature += fmt.Sprintf("%q:%d:%q;", column.String, descending, strings.ToUpper(collation.String))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return result, err
		}
		if err := rows.Close(); err != nil {
			return result, err
		}
		if index.partial != 0 {
			var definition string
			if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='index' AND name=?`, index.name).Scan(&definition); err != nil {
				return result, err
			}
			// PRAGMA index_xinfo does not expose a partial-index predicate.
			// The only supported predicate is this fixed coverage constraint;
			// reject alternatives instead of assuming all partial indexes agree.
			predicate, ok := coverageIndexPredicate(definition)
			if !ok {
				return result, errors.New("backup contains incompatible partial-index predicate")
			}
			signature += "where=" + predicate
		}
		result.Indexes = append(result.Indexes, signature)
	}
	slices.Sort(result.Indexes)
	return result, nil
}

func coverageIndexPredicate(definition string) (string, bool) {
	tokens, err := schemaTokens(definition)
	if err != nil {
		return "", false
	}
	position := -1
	for i, token := range tokens {
		if token.Kind == 'w' && token.Text == "WHERE" {
			position = i
		}
	}
	if position < 0 || len(tokens)-position != 4 {
		return "", false
	}
	for i, wanted := range []string{"ENDED_AT", "IS", "NULL"} {
		if tokens[position+i+1] != (schemaToken{Kind: 'w', Text: wanted}) {
			return "", false
		}
	}
	return "ENDED_AT IS NULL", true
}

func compareSnapshotTable(actual, reference snapshotTable, columnNames string, table string) error {
	expected := strings.Fields(columnNames)
	if len(actual.Columns) != len(expected) {
		return fmt.Errorf("backup schema has incompatible columns in %s", table)
	}
	for _, name := range expected {
		column, exists := actual.Columns[name]
		want, known := reference.Columns[name]
		if !exists || !known || column != want || column.Hidden != 0 {
			return fmt.Errorf("backup schema has incompatible type or constraints for %s.%s", table, name)
		}
	}
	if actual.WithoutRowID != reference.WithoutRowID || actual.Strict != reference.Strict {
		return fmt.Errorf("backup schema has incompatible table mode for %s", table)
	}
	if !slices.Equal(actual.Definition, reference.Definition) {
		return fmt.Errorf("backup schema has incompatible table constraints for %s", table)
	}
	if !slices.Equal(actual.Indexes, reference.Indexes) {
		return fmt.Errorf("backup schema has incompatible indexes for %s", table)
	}
	return nil
}

// Only application-generated table DDL is supported. Token comparison ignores
// whitespace and identifier case/quoting, but preserves string-literal values.
// It also covers CHECK, REFERENCES and collation clauses not exposed by PRAGMAs.
// Comments are rejected instead of being mistaken for constraint predicates.
type schemaToken struct {
	Kind byte
	Text string
}

func schemaTokens(definition string) ([]schemaToken, error) {
	var tokens []schemaToken
	for i := 0; i < len(definition); {
		c := definition[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			i++
			continue
		}
		if i+1 < len(definition) && (definition[i:i+2] == "--" || definition[i:i+2] == "/*") {
			return nil, errors.New("backup schema contains unsupported SQL comments")
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			quote := c
			if quote == '[' {
				quote = ']'
			}
			kind := byte('w')
			if c == '\'' {
				kind = 's'
			}
			i++
			var value strings.Builder
			closed := false
			for i < len(definition) {
				if definition[i] == quote {
					if quote != ']' && i+1 < len(definition) && definition[i+1] == quote {
						value.WriteByte(quote)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				value.WriteByte(definition[i])
				i++
			}
			if !closed {
				return nil, errors.New("backup schema contains an unterminated quoted token")
			}
			text := value.String()
			if kind == 'w' {
				text = strings.ToUpper(text)
			}
			tokens = append(tokens, schemaToken{Kind: kind, Text: text})
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			start := i
			for i < len(definition) {
				c = definition[i]
				if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					break
				}
				i++
			}
			tokens = append(tokens, schemaToken{Kind: 'w', Text: strings.ToUpper(definition[start:i])})
			continue
		}
		tokens = append(tokens, schemaToken{Kind: 'p', Text: string(c)})
		i++
	}
	if len(tokens) > 0 && tokens[len(tokens)-1] == (schemaToken{Kind: 'p', Text: ";"}) {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens, nil
}

func downgradeSnapshotSchemaReference(ctx context.Context, db *sql.DB, version int) error {
	var statements []string
	if version < 7 {
		statements = append(statements, `DROP TABLE journal_recovery`)
	}
	if version < 6 {
		statements = append(statements, `DROP TABLE monitor_state`, `DROP TABLE retention_meta`, `DROP TABLE retention_ledger`, `DROP TABLE retention_totals`)
	}
	if version < 5 {
		statements = append(statements, `ALTER TABLE report_snapshots DROP COLUMN billing_json`)
	}
	if version < 4 {
		statements = append(statements, `DROP TABLE event_notifications`, `DROP TABLE notification_silences`, `DROP TABLE interface_detail_hourly`,
			`DROP INDEX events_incident_idx`, `DROP INDEX events_source_time_idx`, `DROP INDEX notification_incident_idx`)
		for _, column := range []string{"incident_id", "event_kind", "event_phase", "merged_count", "merge_until", "suppressed_at", "lease_until"} {
			statements = append(statements, `ALTER TABLE notification_outbox DROP COLUMN `+column)
		}
	}
	if version < 3 {
		for _, table := range []string{"notification_cooldowns", "notification_counters", "report_snapshots", "coverage_intervals", "coverage_gaps", "collector_checkpoints", "journal_seen"} {
			statements = append(statements, `DROP TABLE `+table)
		}
		statements = append(statements, `ALTER TABLE notification_outbox DROP COLUMN quarantined_at`, `ALTER TABLE notification_outbox DROP COLUMN expires_at`)
	}
	if version < 2 {
		for _, column := range []string{"ipc_dropped_batches", "ipc_dropped_packets", "ipc_dropped_bytes", "health_counter_saturations"} {
			statements = append(statements, `ALTER TABLE collector_health_hourly DROP COLUMN `+column)
		}
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
