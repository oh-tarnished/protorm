package generator

// resolve.go is the second IR pass: after every file has been folded into the
// database, FK references are resolved to real PK columns and HasMany
// back-references are populated so generators have both sides of every relation.

import "github.com/oh-tarnished/protorm/plugin/generator/schema"

// resolveRelations corrects each ForeignKey against the live referenced table:
// the real schema (datasource.schema overrides shift tables away from the
// resource-type-derived schema), the real table name, and the real PK column.
// It also appends a HasManyRef to the referenced table so both relation sides
// exist. A reference to a model not present in the database is recorded on diags
// (a typo'd resource_reference is the usual cause) and falls back to a
// conventional "id" so non-strict generation still produces output.
func resolveRelations(db *schema.Database, diags *diagnostics) {
	type located struct {
		schema *schema.Schema
		table  *schema.Table
	}
	byModel := map[string]located{}
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			byModel[t.ModelName] = located{schema: s, table: t}
		}
	}

	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			for _, fk := range t.ForeignKeys {
				ref, ok := byModel[fk.ReferencedModel]
				if !ok {
					diags.warnf("table %q column %q references unknown model %q "+
						"via resource_reference; FK falls back to column \"id\"",
						t.Name, fk.Column, fk.ReferencedModel)
					fk.ReferencedColumn = "id"
					continue
				}
				fk.ReferencedSchema = ref.schema.Name
				fk.ReferencedTable = ref.table.Name
				if fk.ReferencedColumn = ref.table.PKColumn; fk.ReferencedColumn == "" {
					fk.ReferencedColumn = "id"
				}
				alignFKType(t, fk, ref.table)
				ref.table.HasMany = append(ref.table.HasMany, &schema.HasManyRef{
					Model: t.ModelName, Field: t.Name, ViaFK: fk.Column,
				})
			}
		}
	}
}

// alignFKType makes the referencing column's storage type match the referenced
// PK column (e.g. CHAR(26) when the parent uses a ULID id strategy). Columns
// whose type the user pinned via col.type / max_length / precision are left alone.
func alignFKType(t *schema.Table, fk *schema.ForeignKey, ref *schema.Table) {
	var child, pk *schema.Column
	for _, c := range t.Columns {
		if c.Name == fk.Column {
			child = c
		}
	}
	for _, c := range ref.Columns {
		if c.Name == fk.ReferencedColumn {
			pk = c
		}
	}
	if child == nil || pk == nil || child.TypeOverridden || child.Enum != nil {
		return
	}
	child.SQLType = pk.SQLType
}
