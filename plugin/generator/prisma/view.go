package prisma

// view.go prepares the fragment template view models: every naming and type
// decision happens here so the template stays purely presentational.

import (
	"strings"

	"github.com/oh-tarnished/protorm/plugin/generator/header"
	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	"github.com/oh-tarnished/protorm/plugin/generator/types"
)

type fieldLine struct{ Doc, Decl string }

type modelView struct {
	Comment, Name, Map string
	Fields             []fieldLine
	Indexes            []fieldLine
}

// fragmentView assembles the data for one <domain>.<provider>.prisma fragment:
// the enums and models of schema s that originate from the given source file.
func fragmentView(db *schema.Database, s *schema.Schema, domain string, provider types.Provider) map[string]any {
	var enums []*schema.Enum
	for _, e := range s.Enums {
		if e.SourceFile == domain {
			enums = append(enums, withFallbackComments(e))
		}
	}
	var models []modelView
	srcProto := domain + ".proto"
	for _, t := range s.Tables {
		if t.SourceFile == domain {
			models = append(models, modelViewOf(t, provider))
			if t.SourceProto != "" {
				srcProto = t.SourceProto
			}
		}
	}
	return map[string]any{
		"Header": header.Render("//", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Source:        srcProto,
			Database:      db.Name,
			Schema:        s.Name,
		}),
		"Schema":      s.Name,
		"MultiSchema": provider == types.Postgres,
		"Enums":       enums,
		"Models":      models,
	}
}

// modelViewOf renders one table into template-ready field and index lines.
func modelViewOf(t *schema.Table, provider types.Provider) modelView {
	fkByCol := map[string]*schema.ForeignKey{}
	for _, fk := range t.ForeignKeys {
		fkByCol[fk.Column] = fk
	}

	m := modelView{Comment: commentOr(t.Comment, t.ModelName+" model."), Name: t.ModelName, Map: t.Name}

	for _, col := range t.Columns {
		m.Fields = append(m.Fields, fieldLine{Doc: fieldDoc(col), Decl: fieldDecl(col, provider)})

		// BelongsTo relation — emitted immediately after the FK column.
		if fk, ok := fkByCol[col.Name]; ok {
			relType := fk.ReferencedModel
			if col.Optional {
				relType += "?"
			}
			args := "fields: [" + naming.Camel(col.Name) + "], references: [" +
				naming.Camel(fk.ReferencedColumn) + "]"
			if a := prismaAction(fk.OnDelete); a != "" {
				args += ", onDelete: " + a
			}
			if a := prismaAction(fk.OnUpdate); a != "" {
				args += ", onUpdate: " + a
			}
			m.Fields = append(m.Fields, fieldLine{
				Doc: "Relation to " + fk.ReferencedModel + " via " + col.Name + ".",
				Decl: naming.CamelFirst(fk.ReferencedModel) + " " + relType +
					" @relation(" + args + ")",
			})
		}
	}

	// HasMany back-references (both sides required by Prisma's relation validator).
	for _, hm := range t.HasMany {
		m.Fields = append(m.Fields, fieldLine{
			Doc:  "Back-relation: " + hm.Model + " records that reference this model via " + hm.ViaFK + ".",
			Decl: naming.Camel(hm.Field) + " " + hm.Model + "[]",
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

// fieldDecl renders one column declaration: name, type, and attributes.
func fieldDecl(col *schema.Column, provider types.Provider) string {
	var b strings.Builder
	b.WriteString(naming.Camel(col.Name))
	b.WriteByte(' ')
	typeName := col.SQLType
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
