package generator

// resolve.go is the second IR pass: after every file has been folded into the
// database, FK references are resolved to real PK columns and HasMany
// back-references are populated so generators have both sides of every relation.

import (
	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
)

// resolveRelations corrects each ForeignKey against the live referenced table:
// the real schema (datasource.schema overrides shift tables away from the
// resource-type-derived schema), the real table name, and the real PK column.
// It also appends a HasManyRef to the referenced table so both relation sides
// exist.
//
// A foreign key is dropped (its scalar column kept) when it references a model
// not present in the database — a typo'd resource_reference or a reference to a
// model outside the generate set. Emitting a relation to a non-existent model
// produces a schema that fails Prisma validation, so the relation is removed
// and the issue recorded on diags (a hard error under --strict). A FK whose
// scalar column went missing is likewise dropped defensively.
func resolveRelations(db *schema.Database, diags *diagnostics) {
	type located struct {
		schema *schema.Schema
		table  *schema.Table
	}
	byModel := map[string]located{}
	byProto := map[string]located{}
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			byModel[t.ModelName] = located{schema: s, table: t}
			byProto[t.ProtoMessage] = located{schema: s, table: t}
		}
	}

	// link pairs each resolved FK with the HasManyRef it produced, plus the model
	// pair they connect, so relation names can be assigned once all FKs are known.
	type link struct {
		fk         *schema.ForeignKey
		hm         *schema.HasManyRef
		childModel string
	}
	var links []link

	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			resolved := t.ForeignKeys[:0]
			for _, fk := range t.ForeignKeys {
				if !hasColumn(t, fk.Column) {
					diags.warnf("table %q FK names missing column %q; relation dropped",
						t.Name, fk.Column)
					continue
				}
				// Embed relations pin the exact target message; resource_reference
				// FKs resolve by (possibly qualified) model name.
				ref, ok := byProto[fk.ReferencedProto]
				if fk.ReferencedProto == "" || !ok {
					ref, ok = byModel[fk.ReferencedModel]
				}
				if !ok {
					diags.warnf("table %q column %q references unknown model %q; "+
						"relation dropped (the scalar column is kept)",
						t.Name, fk.Column, fk.ReferencedModel)
					continue
				}
				// Reflect any model-name qualification onto the FK and its column.
				fk.ReferencedModel = ref.table.ModelName
				setColumnFKModel(t, fk.Column, ref.table.ModelName)
				fk.ReferencedSchema = ref.schema.Name
				fk.ReferencedTable = ref.table.Name
				if fk.ReferencedColumn = ref.table.PKColumn; fk.ReferencedColumn == "" {
					fk.ReferencedColumn = "id"
				}
				alignFKType(t, fk, ref.table)
				hm := &schema.HasManyRef{Model: t.ModelName, Field: t.Name, ViaFK: fk.Column}
				ref.table.HasMany = append(ref.table.HasMany, hm)
				resolved = append(resolved, fk)
				links = append(links, link{fk: fk, hm: hm, childModel: t.ModelName})
			}
			t.ForeignKeys = resolved
		}
	}

	// Name relations only where a model pair is connected by more than one
	// relation — Prisma rejects ambiguous unnamed relations, but a single
	// relation per pair needs no name. The name is derived from the child model
	// and the FK column (minus its _id suffix) so it is stable and unique, and is
	// mirrored onto both relation sides.
	pairCount := map[string]int{}
	for _, l := range links {
		pairCount[modelPairKey(l.childModel, l.fk.ReferencedModel)]++
	}
	for _, l := range links {
		if pairCount[modelPairKey(l.childModel, l.fk.ReferencedModel)] < 2 {
			continue
		}
		name := l.childModel + naming.PascalGo(naming.StripIDSuffix(l.fk.Column))
		l.fk.RelationName = name
		l.hm.RelationName = name
	}
}

// modelPairKey is an order-independent key for the two models a relation connects.
func modelPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

// hasColumn reports whether t declares a column named name.
func hasColumn(t *schema.Table, name string) bool {
	for _, c := range t.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

// setColumnFKModel updates the FK column's association model name so targets
// (GORM struct types) track any model-name qualification applied after build.
func setColumnFKModel(t *schema.Table, col, model string) {
	for _, c := range t.Columns {
		if c.Name == col {
			c.FKModel = model
			return
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
