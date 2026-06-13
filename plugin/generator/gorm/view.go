package gorm

// view.go prepares the models.go template view: naming, Go types, struct tags,
// enum consts, and conditional imports. The template is presentation only.

import (
	"strings"

	"github.com/oh-tarnished/protorm/plugin/generator/header"
	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	"github.com/oh-tarnished/protorm/plugin/generator/types"
)

type fieldView struct{ Comment, Decl string }

type modelView struct {
	Comment, Name, TableName string
	Fields                   []fieldView
}

type enumValueView struct{ ConstName, TypeName, MapName string }

type enumView struct {
	Comment, Name string
	Values        []enumValueView
}

// packageView assembles the template data for one schema package.
func packageView(db *schema.Database, s *schema.Schema, pkg string) map[string]any {
	var models []modelView
	needTime, needJSON := false, false

	for _, t := range s.Tables {
		m := modelView{
			Comment:   commentOr(t.Comment, t.ModelName+" model."),
			Name:      t.ModelName,
			TableName: s.Name + "." + t.Name,
		}
		for _, col := range t.Columns {
			gt := goType(col)
			needTime = needTime || strings.Contains(gt, "time.Time")
			needJSON = needJSON || strings.Contains(gt, "json.RawMessage")

			goField := naming.PascalGo(col.Name)
			m.Fields = append(m.Fields, fieldView{
				Comment: col.Comment,
				Decl:    goField + " " + gt + " `" + structTag(col) + "`",
			})
			// BelongsTo association: emitted alongside the FK column.
			if col.FKModel != "" {
				m.Fields = append(m.Fields, fieldView{
					Decl: col.FKModel + " *" + col.FKModel +
						" `gorm:\"foreignKey:" + goField + constraintTag(t, col.Name) +
						"\" json:\"" + strings.ToLower(col.FKModel) + ",omitempty\"`",
				})
			}
		}
		// HasMany back-references (e.g. Author.Books []Book).
		for _, hm := range t.HasMany {
			m.Fields = append(m.Fields, fieldView{
				Comment: "Back-relation: " + hm.Model + " records that reference this via " + hm.ViaFK + ".",
				Decl: naming.PascalGo(hm.Field) + " []" + hm.Model +
					" `gorm:\"foreignKey:" + naming.PascalGo(hm.ViaFK) + "\" json:\"" + strings.ToLower(hm.Field) + ",omitempty\"`",
			})
		}
		models = append(models, m)
	}

	var imports []string
	if needJSON {
		imports = append(imports, "encoding/json")
	}
	if needTime {
		imports = append(imports, "time")
	}

	return map[string]any{
		"Header": header.Render("//", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Source:        strings.Join(s.SourceProtos(), ", "),
			Database:      db.Name,
			Schema:        s.Name,
		}),
		"Package": pkg,
		"Imports": imports,
		"Enums":   enumViews(s),
		"Models":  models,
	}
}

// enumViews renders each schema enum as a Go string type with one const per value.
func enumViews(s *schema.Schema) []enumView {
	var out []enumView
	for _, e := range s.Enums {
		ev := enumView{
			Comment: commentOr(e.Comment, e.Name+" enumerates the "+e.SQLName+" values."),
			Name:    e.Name,
		}
		for _, v := range e.Values {
			ev.Values = append(ev.Values, enumValueView{
				ConstName: e.Name + naming.PascalGo(strings.ToLower(v.Name)),
				TypeName:  e.Name,
				MapName:   v.MapName,
			})
		}
		out = append(out, ev)
	}
	return out
}

// goType returns the Go type for a column: enums use their generated Go type,
// nullable scalars become pointers. Slice types ([]byte, json.RawMessage,
// arrays) are not re-wrapped — their nil zero value already encodes SQL NULL.
func goType(col *schema.Column) string {
	var base string
	if col.Enum != nil {
		base = col.Enum.Name
	} else {
		base = types.GoType(col.SQLType)
	}
	if col.Optional && !strings.HasPrefix(base, "[]") && base != "json.RawMessage" {
		return "*" + base
	}
	return base
}

// structTag builds the combined gorm + json + validate struct tag for a column.
func structTag(col *schema.Column) string {
	gormParts := []string{"column:" + col.Name}
	if col.PrimaryKey {
		gormParts = append(gormParts, "primaryKey")
	}
	if col.NotNull {
		gormParts = append(gormParts, "not null")
	}
	if col.Unique {
		gormParts = append(gormParts, "uniqueIndex")
	}
	switch {
	case col.AutoCreate:
		gormParts = append(gormParts, "autoCreateTime")
	case col.AutoUpdate:
		gormParts = append(gormParts, "autoUpdateTime")
	case col.Generated == "uuid":
		gormParts = append(gormParts, "default:gen_random_uuid()")
	case col.Default != "":
		gormParts = append(gormParts, "default:"+col.Default)
	}
	if col.Index {
		gormParts = append(gormParts, "index")
	}
	tag := `gorm:"` + strings.Join(gormParts, ";") + `"`

	if col.Optional {
		tag += ` json:"` + col.Name + `,omitempty"`
	} else {
		tag += ` json:"` + col.Name + `"`
	}

	// Required validation for non-PK NOT NULL fields the application supplies —
	// DB-managed columns (generated ids, timestamps) are excluded.
	if col.NotNull && !col.PrimaryKey && !col.AutoCreate && !col.AutoUpdate && col.Generated == "" {
		tag += ` validate:"required"`
	}
	return tag
}

// constraintTag renders the GORM constraint fragment for the FK on column
// colName, e.g. ";constraint:OnDelete:CASCADE,OnUpdate:SET NULL". Empty when
// the FK declares no referential actions.
func constraintTag(t *schema.Table, colName string) string {
	for _, fk := range t.ForeignKeys {
		if fk.Column != colName {
			continue
		}
		var parts []string
		if fk.OnDelete != "" {
			parts = append(parts, "OnDelete:"+fk.OnDelete)
		}
		if fk.OnUpdate != "" {
			parts = append(parts, "OnUpdate:"+fk.OnUpdate)
		}
		if len(parts) > 0 {
			return ";constraint:" + strings.Join(parts, ",")
		}
	}
	return ""
}

// commentOr returns comment when non-empty, otherwise the fallback.
func commentOr(comment, fallback string) string {
	if comment != "" {
		return comment
	}
	return fallback
}
