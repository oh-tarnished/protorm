package prisma

// view.go prepares the fragment template view models: every naming and type
// decision happens here so the template stays purely presentational.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/oh-tarnished/protorm/plugin/generator/header"
	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	"github.com/oh-tarnished/protorm/plugin/generator/types"
)

type fieldLine struct{ Doc, Decl string }

type modelView struct {
	Comment, Name, Map, Schema string
	Fields                     []fieldLine
	Indexes                    []fieldLine
}

// fragmentView assembles the data for one source proto's
// <file>.<provider>.prisma fragment. A fragment may span several @@schemas, so
// every model and enum carries its own schema (rendered per block in the
// template) rather than a single fragment-wide schema.
func fragmentView(db *schema.Database, g fragmentGroup, provider types.Provider) map[string]any {
	var enums []*schema.Enum
	for _, e := range g.enums {
		enums = append(enums, withFallbackComments(e))
	}
	models := make([]modelView, 0, len(g.tables))
	for _, t := range g.tables {
		models = append(models, modelViewOf(t, provider))
	}
	srcProto := g.sourceProto
	if srcProto == "" {
		srcProto = g.fileBase + ".proto"
	}
	return map[string]any{
		"Header": header.Render("//", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Source:        srcProto,
			Database:      db.Name,
			SchemaLabel:   "schemas",
			Schema:        strings.Join(fragmentSchemas(g), ", "),
		}),
		"MultiSchema": provider == types.Postgres,
		"Enums":       enums,
		"Models":      models,
	}
}

// fragmentSchemas lists the distinct postgres schemas a fragment's models and
// enums belong to, in deterministic order, for the generated-file banner.
func fragmentSchemas(g fragmentGroup) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, t := range g.tables {
		add(t.PgSchema)
	}
	for _, e := range g.enums {
		add(e.PgSchema)
	}
	sort.Strings(out)
	return out
}

// modelViewOf renders one table into template-ready field and index lines.
func modelViewOf(t *schema.Table, provider types.Provider) modelView {
	fkByCol := map[string]*schema.ForeignKey{}
	for _, fk := range t.ForeignKeys {
		fkByCol[fk.Column] = fk
	}

	m := modelView{Comment: commentOr(t.Comment, t.ModelName+" model."), Name: t.ModelName, Map: t.Name, Schema: t.PgSchema}

	// Reserve every scalar column name so a relation field can't collide with one.
	used := map[string]bool{}
	for _, col := range t.Columns {
		used[naming.Camel(col.Name)] = true
	}

	for _, col := range t.Columns {
		m.Fields = append(m.Fields, fieldLine{Doc: fieldDoc(col), Decl: fieldDecl(col, provider)})

		// BelongsTo relation — emitted immediately after the FK column. The field
		// name is derived from the FK column (minus _id) so two FKs to the same
		// model stay distinct (organizer / creator), with a named @relation when
		// the model pair is ambiguous.
		if fk, ok := fkByCol[col.Name]; ok {
			relType := fk.ReferencedModel
			if col.Optional {
				relType += "?"
			}
			args := ""
			if fk.RelationName != "" {
				args = `"` + fk.RelationName + `", `
			}
			args += "fields: [" + naming.Camel(col.Name) + "], references: [" +
				naming.Camel(fk.ReferencedColumn) + "]"
			if a := prismaAction(fk.OnDelete); a != "" {
				args += ", onDelete: " + a
			}
			if a := prismaAction(fk.OnUpdate); a != "" {
				args += ", onUpdate: " + a
			}
			field := uniqueName(naming.Camel(naming.StripIDSuffix(col.Name)), used)
			m.Fields = append(m.Fields, fieldLine{
				Doc:  "Relation to " + fk.ReferencedModel + " via " + col.Name + ".",
				Decl: field + " " + relType + " @relation(" + args + ")",
			})
		}
	}

	// HasMany back-references (both sides required by Prisma's relation validator).
	for _, hm := range t.HasMany {
		field := uniqueName(naming.Camel(hm.Field), used)
		decl := field + " " + hm.Model + "[]"
		if hm.RelationName != "" {
			decl += ` @relation("` + hm.RelationName + `")`
		}
		m.Fields = append(m.Fields, fieldLine{
			Doc:  "Back-relation: " + hm.Model + " records that reference this model via " + hm.ViaFK + ".",
			Decl: decl,
		})
	}

	for _, idx := range t.Indexes {
		cols := make([]string, len(idx.Columns))
		for i, c := range idx.Columns {
			cols[i] = naming.Camel(c)
		}
		directive, label := "@@index", "Composite index"
		if idx.Unique {
			directive, label = "@@unique", "Unique constraint"
		}
		m.Indexes = append(m.Indexes, fieldLine{
			Doc:  label + " on [" + strings.Join(idx.Columns, ", ") + "].",
			Decl: directive + "([" + strings.Join(cols, ", ") + "])",
		})
	}
	return m
}

// uniqueName returns base, or base with the smallest numeric suffix that is not
// already in used, and records the result. Keeps generated relation field names
// from colliding with scalar columns or one another within a model.
func uniqueName(base string, used map[string]bool) string {
	name := base
	for i := 2; used[name]; i++ {
		name = base + strconv.Itoa(i)
	}
	used[name] = true
	return name
}

// fieldDecl renders one column declaration: name, type, and attributes.
func fieldDecl(col *schema.Column, provider types.Provider) string {
	var b strings.Builder
	b.WriteString(naming.Camel(col.Name))
	b.WriteByte(' ')
	var typeName string
	if col.Enum != nil {
		typeName = col.Enum.Name
	} else {
		typeName = types.PrismaTypeFor(provider, col.SQLType)
	}
	b.WriteString(typeName)
	// A Prisma list is implicitly empty-not-null: an optional list (`Type[]?`)
	// is a schema error, so only scalar columns take the optional marker.
	if col.Optional && !strings.HasSuffix(typeName, "[]") {
		b.WriteByte('?')
	}
	if col.PrimaryKey {
		b.WriteString(" @id")
	}
	if col.Unique {
		b.WriteString(" @unique")
	}
	switch {
	case col.Generated != "":
		b.WriteString(" @default(" + col.Generated + "())") // ulid() / uuid()
	case col.AutoUpdate:
		b.WriteString(" @updatedAt") // Prisma maintains the value; no @default
	case col.Default != "":
		b.WriteString(" @default(" + col.Default + ")")
	}
	mapName := col.Name
	if provider == types.MongoDB && col.PrimaryKey {
		mapName = "_id" // Mongo documents key on _id; Prisma requires the mapping.
	}
	b.WriteString(` @map("` + mapName + `")`)
	return b.String()
}

// prismaAction converts a SQL referential action to Prisma's identifier form:
// "SET NULL" → "SetNull", "CASCADE" → "Cascade". Empty stays empty.
func prismaAction(sqlAction string) string {
	if sqlAction == "" {
		return ""
	}
	var b strings.Builder
	for _, word := range strings.Fields(sqlAction) {
		b.WriteString(strings.ToUpper(word[:1]) + strings.ToLower(word[1:]))
	}
	return b.String()
}

// fieldDoc returns the /// documentation for a column: the proto comment when
// present, otherwise a generated description.
func fieldDoc(col *schema.Column) string {
	if col.Comment != "" {
		return col.Comment
	}
	switch {
	case col.PrimaryKey:
		return `Unique identifier for the record. Primary key mapped to "` + col.Name + `".`
	case col.Optional:
		return `Optional column mapped to "` + col.Name + `".`
	default:
		return `Required column mapped to "` + col.Name + `".`
	}
}

// withFallbackComments fills empty enum/value comments so every line still
// carries /// documentation, matching the hand-written schema convention.
func withFallbackComments(e *schema.Enum) *schema.Enum {
	if e.Comment == "" {
		e.Comment = "Enum representing " + e.Name + " values."
	}
	for _, v := range e.Values {
		if v.Comment == "" {
			v.Comment = "Represents the " + v.MapName + " value."
		}
	}
	return e
}

// commentOr returns comment when non-empty, otherwise the fallback.
func commentOr(comment, fallback string) string {
	if comment != "" {
		return comment
	}
	return fallback
}
