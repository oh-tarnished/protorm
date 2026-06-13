package sql

// view.go prepares the schema.sql template view: column definitions, enum
// CREATE TYPE data, FK constraints, and index statements.

import (
	"fmt"
	"strings"

	"github.com/oh-tarnished/protorm/plugin/generator/header"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
)

// quoteIdent wraps a SQL identifier in double quotes (doubling any embedded
// quote) so table/column/type names that collide with reserved words — order,
// user, window — emit valid DDL instead of silently breaking.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// quoteLiteral wraps a SQL string literal in single quotes, doubling any
// embedded single quote so apostrophes in enum values can't terminate the
// literal. (Column default_value is intentionally not passed through here: it
// is documented as a raw SQL expression, e.g. now(), not a string literal.)
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// qualified joins a schema and object name into a quoted, schema-qualified
// reference: ("kitchen", "sinks") → "kitchen"."sinks".
func qualified(schema, name string) string {
	return quoteIdent(schema) + "." + quoteIdent(name)
}

type itemView struct {
	Comment, Def string
	Last         bool
}

type enumDDLView struct {
	Comment, TypeRef, ValueList string
}

type tableView struct {
	Comment, Ref string
	Items        []itemView
	Indexes      []string
}

// schemaView assembles the template data for one schema's DDL file.
func schemaView(db *schema.Database, s *schema.Schema) map[string]any {
	var enums []enumDDLView
	for _, e := range s.Enums {
		vals := make([]string, len(e.Values))
		for i, v := range e.Values {
			vals[i] = quoteLiteral(v.MapName)
		}
		comment := e.Comment
		if comment == "" {
			comment = e.Name + " enum values."
		}
		enums = append(enums, enumDDLView{
			Comment:   comment,
			TypeRef:   qualified(s.Name, e.SQLName),
			ValueList: strings.Join(vals, ", "),
		})
	}

	var tables []tableView
	for _, t := range s.Tables {
		tables = append(tables, tableViewOf(s, t))
	}

	return map[string]any{
		"Header": header.Render("--", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Source:        strings.Join(s.SourceProtos(), ", "),
			Database:      db.Name,
			Schema:        s.Name,
		}),
		"SchemaQ": quoteIdent(s.Name),
		"Enums":   enums,
		"Tables":  tables,
	}
}

// tableViewOf renders one table's column defs, FK constraints, and indexes.
func tableViewOf(s *schema.Schema, t *schema.Table) tableView {
	tv := tableView{Comment: t.Comment, Ref: qualified(s.Name, t.Name)}

	for _, col := range t.Columns {
		tv.Items = append(tv.Items, itemView{Comment: col.Comment, Def: colDef(s, col)})
	}
	for _, fk := range t.ForeignKeys {
		tv.Items = append(tv.Items, itemView{Def: fkDef(t.Name, fk)})
	}
	if n := len(tv.Items); n > 0 {
		tv.Items[n-1].Last = true
	}

	for _, idx := range t.Indexes {
		name := idx.Name
		if name == "" {
			name = "idx_" + t.Name + "_" + strings.Join(idx.Columns, "_")
		}
		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = quoteIdent(c)
		}
		unique := ""
		if idx.Unique {
			unique = "UNIQUE "
		}
		tv.Indexes = append(tv.Indexes, fmt.Sprintf(
			"CREATE %sINDEX %s ON %s (%s);",
			unique, quoteIdent(name), qualified(s.Name, t.Name), strings.Join(cols, ", "),
		))
	}
	return tv
}

// colDef renders a single column definition fragment.
// Enum columns use the schema-qualified type created by CREATE TYPE.
func colDef(s *schema.Schema, col *schema.Column) string {
	sqlType := col.SQLType
	if col.Enum != nil {
		sqlType = qualified(s.Name, col.Enum.SQLName)
	}
	def := quoteIdent(col.Name) + "  " + sqlType
	if col.NotNull {
		def += "  NOT NULL"
	}
	if col.PrimaryKey {
		def += "  PRIMARY KEY"
	}
	if col.Unique && !col.PrimaryKey {
		def += "  UNIQUE"
	}
	switch {
	case col.Generated == "uuid":
		def += "  DEFAULT gen_random_uuid()"
	case col.Generated == "ulid":
		// PostgreSQL has no native ulid(); the value is generated client-side
		// (Prisma @default(ulid()) or application code).
	case col.Default != "":
		def += "  DEFAULT " + col.Default
	}
	return def
}

// fkDef renders a FOREIGN KEY constraint using the schema-qualified referenced
// table and the actual PK column resolved by the IR build pass.
func fkDef(tableName string, fk *schema.ForeignKey) string {
	refTable := quoteIdent(fk.ReferencedTable)
	if fk.ReferencedSchema != "" {
		refTable = qualified(fk.ReferencedSchema, fk.ReferencedTable)
	}
	def := fmt.Sprintf(
		"CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s)",
		quoteIdent("fk_"+tableName+"_"+fk.Column), quoteIdent(fk.Column),
		refTable, quoteIdent(fk.ReferencedColumn),
	)
	if fk.OnDelete != "" {
		def += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" {
		def += " ON UPDATE " + fk.OnUpdate
	}
	return def
}
