package generator

// embed.go makes the proto→IR mapping lossless for nested data messages.
//
// The base build pass (build.go) turns only google.api.resource messages into
// tables and renders any message-typed field as a JSONB/Json blob. That drops
// the structure of embedded messages (a calendar Event's Location, Schedule,
// Recurrence, …) and produces almost no relations. Here we instead:
//
//  1. record every normalizable message-typed field as an "embed request"
//     during the build (no JSONB column is emitted for it), then
//  2. walk those requests, materializing each referenced message as its own
//     table (reachable-from-resource ⇒ a model) and synthesizing the scalar FK
//     column + foreign key that connects parent and child.
//
// Singular field → belongs-to (FK on the parent). Repeated field → has-many
// (FK on the child pointing back at the parent). Cycles terminate because a
// message is materialized at most once per database.

import (
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"

	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
)

// buildCtx carries the cross-file state the lossless build needs: every proto
// message by full name (so a child defined in another file can be materialized)
// and the queue of embed requests collected while building tables.
type buildCtx struct {
	msgIndex map[string]*protogen.Message
	embeds   []*embedReq
}

// embedReq is one message-typed field that must become a relation.
type embedReq struct {
	db         *schema.Database
	schemaName string          // schema the parent lives in; the child joins it
	parent     *schema.Table   // table carrying the field
	field      *protogen.Field // the message-typed field
	targetMsg  string          // referenced message full name
	repeated   bool
	optional   bool
	onDelete   string
	onUpdate   string
}

// newBuildCtx indexes every message (including nested) in the generate-flagged
// files so embedded children can be located regardless of which file defines them.
func newBuildCtx(p *protogen.Plugin) *buildCtx {
	idx := map[string]*protogen.Message{}
	var walk func(msgs []*protogen.Message)
	walk = func(msgs []*protogen.Message) {
		for _, m := range msgs {
			idx[string(m.Desc.FullName())] = m
			walk(m.Messages)
		}
	}
	for _, f := range p.Files {
		if f.Generate {
			walk(f.Messages)
		}
	}
	return &buildCtx{msgIndex: idx}
}

// normalizableMessage returns the referenced message's full name when field f is
// a user-defined message that should be normalized into a related table, or ""
// otherwise. Maps, scalar/enum fields, and well-known google.* messages (which
// map to native SQL types or JSONB) are not normalized.
func normalizableMessage(f *protogen.Field) string {
	md := f.Desc.Message()
	if md == nil || f.Desc.IsMap() {
		return ""
	}
	full := string(md.FullName())
	if strings.HasPrefix(full, "google.") {
		return ""
	}
	return full
}

// isRequiredField reports whether f carries REQUIRED or IDENTIFIER field_behavior.
func isRequiredField(f *protogen.Field) bool {
	for _, b := range fieldBehaviors(f) {
		if b == annotations.FieldBehavior_REQUIRED || b == annotations.FieldBehavior_IDENTIFIER {
			return true
		}
	}
	return false
}

// normalizeEmbeds drains the embed queue, materializing child tables and wiring
// FK columns. Processing a child may enqueue further embeds (its own message
// fields), so it loops until the queue is empty; each message is materialized
// once per database, which also breaks reference cycles.
func (ctx *buildCtx) normalizeEmbeds(diags *diagnostics) {
	for i := 0; i < len(ctx.embeds); i++ {
		req := ctx.embeds[i]
		msg := ctx.msgIndex[req.targetMsg]
		if msg == nil {
			diags.warnf("table %q field %q references message %q which is not in the "+
				"generate set; left unmapped", req.parent.Name, req.field.Desc.Name(), req.targetMsg)
			continue
		}
		child := ctx.materialize(req.db, req.schemaName, msg)

		if req.repeated {
			// has-many: the child carries a FK back to the parent.
			fkCol := naming.SnakeCase(req.parent.ModelName) + "_id"
			addFKColumn(child, fkCol, req.parent.ModelName, req.parent.ProtoMessage, true, req.onDelete, req.onUpdate)
		} else {
			// belongs-to: the parent carries the FK to the child.
			fkCol := string(req.field.Desc.Name()) + "_id"
			addFKColumn(req.parent, fkCol, child.ModelName, child.ProtoMessage, !req.optional, req.onDelete, req.onUpdate)
		}
	}
}

// materialize returns the table for msg in db, building it (under schemaName)
// on first sight. The build records any further embeds the child introduces.
func (ctx *buildCtx) materialize(db *schema.Database, schemaName string, msg *protogen.Message) *schema.Table {
	full := string(msg.Desc.FullName())
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			if t.ProtoMessage == full {
				return t
			}
		}
	}

	s := schemaByName(db, schemaName)
	srcPath := msg.Desc.ParentFile().Path()
	tOpts := tableOpts(msg)
	name := tOpts.GetTable()
	if name == "" {
		name = naming.SnakePlural(string(msg.Desc.Name()))
	}
	t := &schema.Table{
		Name:         name,
		Comment:      cleanComment(msg.Comments.Leading),
		ModelName:    string(msg.Desc.Name()),
		ProtoMessage: full,
		SourceFile:   sourceFileBase(srcPath),
		SourceProto:  srcPath,
		SourceDir:    protoDirNoVersion(srcPath),
		PgSchema:     schemaName,
	}
	// Append before populating columns so a self/cyclic reference finds the table.
	s.Tables = append(s.Tables, t)
	ctx.populateColumns(db, s, t, msg)
	applyIDStrategy(t, tOpts.GetId())
	applyTimestamps(t, tOpts.GetTimestamps())
	ensurePK(t)
	return t
}

// ensurePK guarantees the table has a primary key so FK references resolve:
// an existing IDENTIFIER PK is kept; otherwise an existing "id" column is
// promoted; otherwise a ULID `id` column is synthesized.
func ensurePK(t *schema.Table) {
	if t.PKColumn != "" {
		return
	}
	for _, c := range t.Columns {
		if c.Name == "id" {
			c.PrimaryKey, c.NotNull, c.Optional = true, true, false
			t.PKColumn = "id"
			return
		}
	}
	id := &schema.Column{
		Name: "id", Comment: "Unique identifier for the record.",
		PrimaryKey: true, NotNull: true, SQLType: "CHAR(26)", Generated: "ulid",
	}
	t.Columns = append([]*schema.Column{id}, t.Columns...)
	t.PKColumn = "id"
}

// addFKColumn appends a synthesized scalar FK column and its ForeignKey to t,
// unless the column already exists (idempotent for multi-parent children).
// refProto pins the exact target message so resolution survives same-named
// models. ReferencedColumn/type are finalized by resolveRelations.
func addFKColumn(t *schema.Table, col, refModel, refProto string, notNull bool, onDelete, onUpdate string) {
	for _, c := range t.Columns {
		if c.Name == col {
			return
		}
	}
	t.Columns = append(t.Columns, &schema.Column{
		Name:     col,
		Comment:  "Foreign key to " + refModel + ".",
		SQLType:  "CHAR(26)",
		NotNull:  notNull,
		Optional: !notNull,
		FKModel:  refModel,
	})
	t.ForeignKeys = append(t.ForeignKeys, &schema.ForeignKey{
		Column:          col,
		ReferencedModel: refModel,
		ReferencedProto: refProto,
		OnDelete:        onDelete,
		OnUpdate:        onUpdate,
	})
}
