package generator

// build.go traverses the proto descriptor set and assembles the schema IR.
// Files declaring the same datasource name merge into one schema.Database.
// FK and HasMany wiring completes in resolve.go after all files are processed.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"

	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	"github.com/oh-tarnished/protorm/plugin/generator/types"
	"github.com/oh-tarnished/protorm/protorm/protormpbv1"
)

// buildDatabases converts every generate-flagged file in the plugin request
// into the database IR, merging files that share a datasource name. Recoverable
// schema problems (unresolved FKs, unknown index columns) are recorded on diags
// rather than failing here; the caller decides their severity via --strict.
func buildDatabases(p *protogen.Plugin, diags *diagnostics) ([]*schema.Database, error) {
	byName := map[string]*schema.Database{}
	var order []*schema.Database
	ctx := newBuildCtx(p)

	for _, f := range p.Files {
		if !f.Generate {
			continue
		}
		db, err := ctx.mergeFile(byName, f)
		if err != nil {
			return nil, err
		}
		if db != nil && byName[db.Name] == db && !contains(order, db) {
			order = append(order, db)
		}
	}

	// Materialize embedded child tables and their FK columns before relations
	// are resolved, so resolveRelations sees the full table set.
	ctx.normalizeEmbeds(diags)

	for _, db := range order {
		qualifyModels(db, diags)
		resolveRelations(db, diags)
		dedupeEnums(db, diags)
		validateIndexes(db, diags)
	}
	return order, nil
}

// qualifyModels enforces the Prisma rule that model names occupy one global
// namespace per database (independent of @@schema). Normalizing embedded value
// types pulls same-named messages (a per-package "Media", "Location", …) into
// one database; here the colliding ones gain a schema-domain prefix
// ("calendar" + "Media" → "CalendarMedia") so every model name is unique. Runs
// before resolveRelations, which then reflects the new names onto every FK.
func qualifyModels(db *schema.Database, diags *diagnostics) {
	byName := map[string][]*schema.Table{}
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			byName[t.ModelName] = append(byName[t.ModelName], t)
		}
	}
	used := map[string]bool{}
	for name, group := range byName {
		if len(group) < 2 {
			used[name] = true
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := byName[name]
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			if group[i].PgSchema != group[j].PgSchema {
				return group[i].PgSchema < group[j].PgSchema
			}
			return group[i].ProtoMessage < group[j].ProtoMessage
		})
		used[group[0].ModelName] = true // first occurrence keeps the bare name
		for _, t := range group[1:] {
			base := naming.PascalGo(naming.SchemaDomain(t.PgSchema)) + t.ModelName
			q := base
			for n := 2; used[q]; n++ {
				q = base + strconv.Itoa(n)
			}
			used[q] = true
			diags.warnf("model %q is defined in multiple schemas; qualified to %q "+
				"(Prisma model names are global)", t.ModelName, q)
			t.ModelName = q
		}
	}
}

// dedupeEnums enforces the Prisma rule that enum type names occupy one global
// namespace per database (independent of @@schema). It (1) collapses the same
// proto enum built separately under multiple schemas onto a single canonical
// definition, repointing every column to it, and (2) qualifies the names of
// distinct enums that happen to share a simple name with a schema-derived
// prefix ("State" in calendar_app and alarm_app → "State" / "AlarmAppState").
func dedupeEnums(db *schema.Database, diags *diagnostics) {
	// Pass 1: choose one canonical *Enum per proto full name (first seen wins).
	canonical := map[string]*schema.Enum{}
	for _, s := range db.Schemas {
		for _, e := range s.Enums {
			if _, ok := canonical[e.ProtoName]; !ok {
				canonical[e.ProtoName] = e
			}
		}
	}
	// Repoint every enum column to its canonical definition, then keep only the
	// canonical enum in its home schema and drop the duplicates elsewhere.
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			for _, c := range t.Columns {
				if c.Enum != nil {
					c.Enum = canonical[c.Enum.ProtoName]
				}
			}
		}
		kept := s.Enums[:0]
		for _, e := range s.Enums {
			if canonical[e.ProtoName] == e {
				kept = append(kept, e)
			}
		}
		s.Enums = kept
	}

	// Pass 2: qualify simple-name collisions among distinct enums with a
	// schema-domain prefix ("calendar" + "State" → "CalendarState"). Sorted for
	// determinism; the first keeps the bare name, the rest gain a unique prefix.
	byName := map[string][]*schema.Enum{}
	for _, e := range canonical {
		byName[e.Name] = append(byName[e.Name], e)
	}
	used := map[string]bool{}
	for name, group := range byName {
		if len(group) < 2 {
			used[name] = true
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := byName[name]
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			if group[i].PgSchema != group[j].PgSchema {
				return group[i].PgSchema < group[j].PgSchema
			}
			return group[i].ProtoName < group[j].ProtoName
		})
		used[group[0].Name] = true // first occurrence keeps the bare name
		for _, e := range group[1:] {
			base := naming.PascalGo(naming.SchemaDomain(e.PgSchema)) + e.Name
			q := base
			for n := 2; used[q]; n++ {
				q = base + strconv.Itoa(n)
			}
			used[q] = true
			diags.warnf("enum %q is defined in multiple schemas; qualified to %q "+
				"(Prisma enum names are global)", name, q)
			e.Name = q
			e.SQLName = naming.SnakeCase(q)
		}
	}
}

// validateIndexes reports any index that names a column absent from its table,
// so a typo in protorm.v1.table.indexes is caught at generation time instead of
// surfacing as invalid DDL when the schema is applied.
func validateIndexes(db *schema.Database, diags *diagnostics) {
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			cols := make(map[string]bool, len(t.Columns))
			for _, c := range t.Columns {
				cols[c.Name] = true
			}
			for _, idx := range t.Indexes {
				label := idx.Name
				if label == "" {
					label = "(" + strings.Join(idx.Columns, ",") + ")"
				}
				for _, c := range idx.Columns {
					if !cols[c] {
						diags.warnf("table %q index %s names unknown column %q",
							t.Name, label, c)
					}
				}
			}
		}
	}
}

func contains(dbs []*schema.Database, db *schema.Database) bool {
	for _, d := range dbs {
		if d == db {
			return true
		}
	}
	return false
}

// mergeFile folds one proto file into the database keyed by its datasource
// name, creating the database on first sight. Returns nil when the file has
// no resource-annotated messages.
func (ctx *buildCtx) mergeFile(byName map[string]*schema.Database, f *protogen.File) (*schema.Database, error) {
	ds := datasourceOpts(f)
	name := ds.GetDatabase()
	if name == "" {
		parts := strings.Split(string(f.Desc.Package()), ".")
		name = parts[len(parts)-1]
	}
	provider, err := types.ParseProvider(ds.GetProvider())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Desc.Path(), err)
	}

	db, ok := byName[name]
	if !ok {
		db = &schema.Database{Name: name, URL: ds.GetUrl(), Provider: string(provider)}
		byName[name] = db
	} else {
		if db.URL == "" {
			db.URL = ds.GetUrl()
		}
		if db.Provider != string(provider) && ds.GetProvider() != "" {
			return nil, fmt.Errorf("%s: datasource %q provider %q conflicts with %q",
				f.Desc.Path(), name, provider, db.Provider)
		}
	}

	if added := ctx.addFileTables(db, f, ds.GetSchema()); !added && !ok {
		delete(byName, name)
		return nil, nil
	}
	return db, nil
}

// addFileTables appends every resource-annotated message in f to db.
// schemaOverride, when non-empty, replaces the resource-type-derived schema
// for all tables in this file. Reports whether anything was added.
func (ctx *buildCtx) addFileTables(db *schema.Database, f *protogen.File, schemaOverride string) bool {
	srcPath := f.Desc.Path()
	src := sourceFileBase(srcPath)
	added := false

	for _, msg := range f.Messages {
		if msg.Desc.IsMapEntry() {
			continue
		}
		tOpts := tableOpts(msg)
		if tOpts.GetSkip() {
			continue
		}
		res := resourceOf(msg)
		if res == nil {
			continue
		}
		sName, tName := schemaTable(res.GetType(), tOpts.GetTable())
		// google.api.resource.plural is the authoritative plural ("shelves"),
		// beating the naive +s inference. protorm.v1.table.table still wins.
		if tOpts.GetTable() == "" && res.GetPlural() != "" {
			tName = naming.SnakeCase(res.GetPlural())
		}
		if schemaOverride != "" {
			sName = schemaOverride
		}
		s := schemaByName(db, sName)
		t := ctx.buildTable(db, s, msg, tName, src, srcPath)
		t.PgSchema = sName
		t.SourceDir = protoDirNoVersion(srcPath)
		s.Tables = append(s.Tables, t)
		added = true
	}
	return added
}

// startsWithLetter reports whether s begins with an ASCII letter — the leading
// character Prisma requires for an enum value identifier.
func startsWithLetter(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// schemaByName returns the named schema in db, creating it on first use.
func schemaByName(db *schema.Database, name string) *schema.Schema {
	for _, s := range db.Schemas {
		if s.Name == name {
			return s
		}
	}
	s := &schema.Schema{Name: name}
	db.Schemas = append(db.Schemas, s)
	return s
}

// buildTable maps one resource-annotated message to a *schema.Table.
func (ctx *buildCtx) buildTable(db *schema.Database, s *schema.Schema, msg *protogen.Message, name, src, srcPath string) *schema.Table {
	t := &schema.Table{
		Name:         name,
		Comment:      cleanComment(msg.Comments.Leading),
		ModelName:    string(msg.Desc.Name()),
		ProtoMessage: string(msg.Desc.FullName()),
		SourceFile:   src,
		SourceProto:  srcPath,
	}

	ctx.populateColumns(db, s, t, msg)

	tOpts := tableOpts(msg)
	applyIDStrategy(t, tOpts.GetId())
	applyTimestamps(t, tOpts.GetTimestamps())

	for _, idx := range tOpts.GetIndexes() {
		t.Indexes = append(t.Indexes, &schema.Index{
			Name: idx.GetIndex(), Columns: idx.GetColumns(), Unique: idx.GetUnique(),
		})
	}
	return t
}

// populateColumns maps msg's fields onto t. Scalar/enum fields become columns;
// string fields with google.api.resource_reference become FK columns; and
// user message-typed fields become embed requests (normalized into related
// tables by normalizeEmbeds) instead of lossy JSONB blobs — unless the field
// is skipped or pins an explicit column type. Shared by buildTable (resources)
// and materialize (embedded children).
func (ctx *buildCtx) populateColumns(db *schema.Database, s *schema.Schema, t *schema.Table, msg *protogen.Message) {
	for _, f := range msg.Fields {
		cOpts := colOpts(f)
		if target := normalizableMessage(f); target != "" && cOpts.GetType() == "" {
			if cOpts.GetSkip() {
				continue
			}
			ctx.embeds = append(ctx.embeds, &embedReq{
				db: db, schemaName: s.Name, parent: t, field: f,
				targetMsg: target, repeated: f.Desc.IsList(),
				optional: !isRequiredField(f),
				onDelete: refAction(cOpts.GetOnDelete()),
				onUpdate: refAction(cOpts.GetOnUpdate()),
			})
			continue
		}

		col := buildColumn(s, f)
		if col == nil {
			continue
		}
		t.Columns = append(t.Columns, col)
		if col.PrimaryKey && t.PKColumn == "" {
			t.PKColumn = col.Name
		}
		if ref := resourceRef(f); ref != nil {
			refSchema, refTable := schemaTable(ref.GetType(), "")
			refModel := modelNameFromType(ref.GetType())
			col.FKModel = refModel
			t.ForeignKeys = append(t.ForeignKeys, &schema.ForeignKey{
				Column:           col.Name,
				ReferencedSchema: refSchema,
				ReferencedTable:  refTable,
				ReferencedModel:  refModel,
				OnDelete:         refAction(cOpts.GetOnDelete()),
				OnUpdate:         refAction(cOpts.GetOnUpdate()),
				// ReferencedColumn filled by resolveRelations after all tables built.
			})
		}
	}
}

// applyIDStrategy synthesizes a generated `id` PK column and demotes any
// IDENTIFIER-derived primary key to a UNIQUE constraint.
func applyIDStrategy(t *schema.Table, st protormpbv1.IdStrategy) {
	if st == protormpbv1.IdStrategy_ID_STRATEGY_UNSPECIFIED {
		return
	}
	for _, c := range t.Columns {
		if c.PrimaryKey {
			c.PrimaryKey, c.Unique = false, true
		}
	}
	id := &schema.Column{
		Name:       "id",
		Comment:    "Unique identifier for the record.",
		PrimaryKey: true,
		NotNull:    true,
	}
	switch st {
	case protormpbv1.IdStrategy_ID_STRATEGY_ULID:
		id.SQLType, id.Generated = "CHAR(26)", "ulid"
	case protormpbv1.IdStrategy_ID_STRATEGY_UUID:
		id.SQLType, id.Generated = "UUID", "uuid"
	}
	t.Columns = append([]*schema.Column{id}, t.Columns...)
	t.PKColumn = "id"
}

// applyTimestamps appends created_at / updated_at TIMESTAMPTZ columns.
func applyTimestamps(t *schema.Table, on bool) {
	if !on {
		return
	}
	t.Columns = append(t.Columns,
		&schema.Column{
			Name: "created_at", Comment: "Timestamp when the record was created.",
			SQLType: "TIMESTAMPTZ", NotNull: true, Default: "now()", AutoCreate: true,
		},
		&schema.Column{
			Name: "updated_at", Comment: "Timestamp when the record was last updated.",
			SQLType: "TIMESTAMPTZ", NotNull: true, Default: "now()", AutoUpdate: true,
		},
	)
}

// refAction converts a ReferentialAction enum to its SQL clause form.
func refAction(a protormpbv1.ReferentialAction) string {
	switch a {
	case protormpbv1.ReferentialAction_REFERENTIAL_ACTION_CASCADE:
		return "CASCADE"
	case protormpbv1.ReferentialAction_REFERENTIAL_ACTION_RESTRICT:
		return "RESTRICT"
	case protormpbv1.ReferentialAction_REFERENTIAL_ACTION_SET_NULL:
		return "SET NULL"
	case protormpbv1.ReferentialAction_REFERENTIAL_ACTION_SET_DEFAULT:
		return "SET DEFAULT"
	case protormpbv1.ReferentialAction_REFERENTIAL_ACTION_NO_ACTION:
		return "NO ACTION"
	default:
		return ""
	}
}

// buildColumn maps one proto field to a *schema.Column.
// Returns nil when the field carries protorm.v1.col.skip = true.
func buildColumn(s *schema.Schema, f *protogen.Field) *schema.Column {
	cOpts := colOpts(f)
	if cOpts.GetSkip() {
		return nil
	}
	col := &schema.Column{
		Name:    colName(f, cOpts),
		Comment: cleanComment(f.Comments.Leading),
		Default: cOpts.GetDefaultValue(),
		Unique:  cOpts.GetUnique(),
		Index:   cOpts.GetIndex(),
	}
	switch {
	case cOpts.GetType() != "":
		col.SQLType, col.TypeOverridden = cOpts.GetType(), true // beats all inference
	case cOpts.GetMaxLength() > 0:
		col.SQLType = fmt.Sprintf("VARCHAR(%d)", cOpts.GetMaxLength())
		col.TypeOverridden = true
	case cOpts.GetPrecision() > 0:
		col.SQLType = fmt.Sprintf("NUMERIC(%d,%d)", cOpts.GetPrecision(), cOpts.GetScale())
		col.TypeOverridden = true
	case f.Enum != nil && !f.Desc.IsList():
		col.Enum = enumByName(s, f.Enum)
	default:
		col.SQLType = types.PostgresType(f)
	}
	for _, b := range fieldBehaviors(f) {
		switch b {
		case annotations.FieldBehavior_REQUIRED:
			col.NotNull = true
		case annotations.FieldBehavior_IDENTIFIER:
			col.PrimaryKey, col.NotNull = true, true
		}
	}
	col.Optional = !col.NotNull
	return col
}

// enumByName returns the IR enum for e within schema s, building it on first use.
func enumByName(s *schema.Schema, e *protogen.Enum) *schema.Enum {
	name := string(e.Desc.Name())
	for _, ex := range s.Enums {
		if ex.Name == name {
			return ex
		}
	}
	en := &schema.Enum{
		Name:        name,
		SQLName:     naming.SnakeCase(name),
		ProtoName:   string(e.Desc.FullName()),
		PgSchema:    s.Name,
		Comment:     cleanComment(e.Comments.Leading),
		SourceFile:  sourceFileBase(e.Desc.ParentFile().Path()),
		SourceProto: e.Desc.ParentFile().Path(),
		SourceDir:   protoDirNoVersion(e.Desc.ParentFile().Path()),
	}
	for _, v := range e.Values {
		full := string(v.Desc.Name())
		// MapName is the SCREAMING_SNAKE stored value (read by every target). Name
		// is the rendered identifier and must start with a letter (a Prisma enum
		// value rule): when stripping the enum prefix leaves a digit-leading value
		// (ASPECT_RATIO_3_4 → "3_4"), fall back to the full proto value name, which
		// is letter-leading. MapName keeps the short form for the DB ("3_4").
		mapName := naming.ScreamingSnake(naming.EnumValueName(name, full))
		valName := mapName
		if !startsWithLetter(valName) {
			valName = naming.ScreamingSnake(full)
		}
		if !startsWithLetter(valName) {
			valName = "V" + valName // rare: a proto value that itself isn't letter-leading
		}
		en.Values = append(en.Values, &schema.EnumValue{
			Name:    valName,
			MapName: mapName,
			Comment: cleanComment(v.Comments.Leading),
		})
	}
	s.Enums = append(s.Enums, en)
	return en
}

// schemaTable parses a google.api.resource.type string (e.g. "bookstore.v1/Book")
// into a schema name ("bookstore_v1") and snake_plural table name ("books").
// nameOverride replaces the inferred table name when non-empty.
func schemaTable(resourceType, nameOverride string) (sName, tName string) {
	parts := strings.SplitN(resourceType, "/", 2)
	if len(parts) != 2 {
		return "public", naming.SnakePlural(resourceType)
	}
	sName = strings.ReplaceAll(strings.ToLower(parts[0]), ".", "_")
	tName = naming.SnakePlural(parts[1])
	if nameOverride != "" {
		tName = nameOverride
	}
	return
}
