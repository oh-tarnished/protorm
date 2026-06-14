// Package schema defines the intermediate representation (IR) that every
// code-generation backend consumes. The IR is built once from the proto
// descriptor set, then each target renders it independently.
//
// Inference sources (highest → lowest priority):
//  1. google.api.resource   → Database, Schema, Table names
//  2. google.api.field_behavior → NotNull, PrimaryKey on Column
//  3. google.api.resource_reference → ForeignKey
//  4. protorm.v1.datasource / .table / .col → overrides for anything AIP can't express
package schema

import (
	"sort"

	"google.golang.org/protobuf/compiler/protogen"
)

// Target is implemented by every output backend (prisma, gorm, sql, csv).
// It receives the fully-built IR and writes output via protogen.Plugin.
type Target interface {
	// Name returns the short identifier matched against the "target" plugin opt.
	// Example: "prisma", "gorm", "sql", "csv".
	Name() string

	// Generate writes one or more output files for every database in dbs.
	Generate(p *protogen.Plugin, dbs []*Database) error
}

// Database is the top-level output unit. Proto files that declare the same
// datasource name merge into a single Database, so a multi-file proto package
// produces one schema tree.
type Database struct {
	Name     string    // e.g. "bookstore_db"
	URL      string    // connection URL; from (protorm.v1.datasource).url
	Provider string    // "postgres" (default) or "mongodb"; from (protorm.v1.datasource).provider
	Schemas  []*Schema // grouped by datasource.schema or resource.type prefix

	// PluginVersion and ProtocVersion fill the generated-file banner. Set by the
	// orchestrator from the build ldflags and the CodeGeneratorRequest; an empty
	// value renders as "(unknown)".
	PluginVersion string
	ProtocVersion string
}

// Schema groups tables that share a namespace. Maps to a PostgreSQL schema.
type Schema struct {
	Name   string   // e.g. "bookstore_v1"
	Tables []*Table // every resource-annotated message under this namespace
	Enums  []*Enum  // every proto enum referenced by a column in this namespace
}

// SourceProtos returns the distinct proto import paths contributing tables to
// the schema, in sorted order — used for the generated-file banner's source
// line when one output merges several protos.
func (s *Schema) SourceProtos() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range s.Tables {
		if t.SourceProto != "" && !seen[t.SourceProto] {
			seen[t.SourceProto] = true
			out = append(out, t.SourceProto)
		}
	}
	sort.Strings(out)
	return out
}

// Table maps one proto message (carrying google.api.resource) to one DB table.
type Table struct {
	// Name is the snake_case plural table name; overridable via protorm.v1.table.name.
	Name string

	// Comment is the proto message's leading comment, normalized for embedding in
	// generated output. Stripped of // markers and trimmed to a single line.
	Comment string

	// ModelName is the singular Go/Prisma model name derived from the proto message
	// simple name. "bookstore.v1.Author" → "Author". Always PascalCase.
	ModelName string

	// PKColumn is the name of the primary key column resolved during build.
	// Referenced by FK resolution to emit correct REFERENCES clauses.
	PKColumn string

	// ProtoMessage is the fully qualified proto message name for diagnostics.
	ProtoMessage string

	// SourceFile is the proto file base name this table came from ("bookstore"
	// for bookstore/v1/bookstore.proto). Drives fragment file splitting:
	// <db>/<schema>/<SourceFile>.<provider>.prisma.
	SourceFile string

	// SourceProto is the full proto import path ("bookstore/v1/bookstore.proto"),
	// shown on the generated-file banner's source line.
	SourceProto string

	// SourceDir is the proto file's directory with the version segment dropped
	// ("store/apps/.../calendar/v1/event.proto" → "store/apps/.../calendar"),
	// driving the mirrored output path so the Prisma tree matches the proto tree.
	SourceDir string

	// PgSchema is the postgres schema this table is placed in. Lets one proto
	// file contribute models to several schemas (per-table @@schema override).
	PgSchema string

	Columns     []*Column
	Indexes     []*Index
	ForeignKeys []*ForeignKey
	HasMany     []*HasManyRef
}

// Column maps one proto field to one database column.
type Column struct {
	// Name is the column identifier (snake_case). Overridable via protorm.v1.col.name.
	Name string

	// Comment is the proto field's leading comment, normalized for embedding.
	Comment string

	// SQLType is the normalized PostgreSQL type (e.g. "VARCHAR(255)", "TIMESTAMPTZ").
	// Canonical across targets: each backend maps it to its own type system.
	// Overridable via protorm.v1.col.type. Empty when Enum is set.
	SQLType string

	// Enum points to the enum definition when the proto field is an enum kind.
	// Backends render their native enum reference instead of SQLType.
	Enum *Enum

	// NotNull is true when the field carries REQUIRED or IDENTIFIER field_behavior.
	NotNull bool

	// PrimaryKey is true when the field carries IDENTIFIER field_behavior.
	PrimaryKey bool

	// Optional is the inverse of NotNull. When true, Go generators emit pointer
	// types (*string, *int32, …) so the zero value is distinguishable from absence.
	Optional bool

	// Unique is true when protorm.v1.col.unique = true.
	Unique bool

	// Default is the SQL default expression from protorm.v1.col.default_value.
	Default string

	// Index is true when protorm.v1.col.index = true (single-column index).
	Index bool

	// TypeOverridden is true when the user pinned the type via col.type,
	// col.max_length, or col.precision. FK type alignment skips such columns.
	TypeOverridden bool

	// Generated names the value-generation strategy for synthesized PK columns:
	// "ulid" or "uuid". Empty for ordinary columns.
	Generated string

	// AutoCreate marks a synthesized created_at column (GORM autoCreateTime).
	AutoCreate bool

	// AutoUpdate marks a synthesized updated_at column
	// (Prisma @updatedAt, GORM autoUpdateTime).
	AutoUpdate bool

	// FKModel is the singular Go model name of the resource this column references.
	// Non-empty only when google.api.resource_reference is present on the field.
	// Example: "author_id" with ref to "bookstore.v1/Author" → FKModel = "Author".
	FKModel string
}

// Enum maps one proto enum referenced by a column to a database enum type.
type Enum struct {
	// Name is the PascalCase enum type name from the proto enum simple name.
	// Globally unique within a database: when two distinct proto enums share a
	// simple name, the later one is qualified with a schema-derived prefix
	// (Prisma enums occupy one global namespace regardless of @@schema).
	Name string

	// SQLName is the snake_case type name used in DDL ("Genre" → "genre").
	SQLName string

	// ProtoName is the fully-qualified proto enum name ("bookstore.v1.Genre"),
	// used to deduplicate the same enum referenced from multiple schemas.
	ProtoName string

	// PgSchema is the postgres schema this enum is placed in (its home schema),
	// used for the per-model/per-enum @@schema directive.
	PgSchema string

	// Comment is the proto enum's leading comment, normalized for embedding.
	Comment string

	// SourceFile is the proto file base name the enum was declared in.
	SourceFile string

	// SourceProto is the full proto import path the enum was declared in.
	SourceProto string

	// SourceDir is the enum's proto directory with the version segment dropped,
	// mirroring Table.SourceDir for fragment file placement.
	SourceDir string

	Values []*EnumValue
}

// EnumValue is a single member of an Enum.
type EnumValue struct {
	// Name is the proto value name with the enum-name prefix stripped
	// ("GENRE_FICTION" → "FICTION"). Stays SCREAMING_SNAKE for Prisma/Go consts.
	Name string

	// MapName is the canonical SCREAMING_SNAKE storage form written to the
	// database ("FICTION"), identical across the Prisma, GORM, and SQL targets.
	// Equals Name except when Name was sanitized to a valid identifier (a
	// digit-leading value: Name "_9S", MapName "9S").
	MapName string

	// Comment is the proto value's leading comment, normalized for embedding.
	Comment string
}

// HasManyRef records a back-reference from a parent table to a child table that
// holds a FK pointing back at the parent. Populated during the build second pass.
// Generators that need both sides of a relation (e.g. Prisma) consume this.
type HasManyRef struct {
	// Model is the child model name that owns the FK (e.g. "Book").
	Model string

	// Field is the snake_plural table name of the child, used to derive the
	// back-relation field name (e.g. "books" → Prisma: "books Book[]").
	Field string

	// ViaFK is the FK column name in the child table (snake_case, e.g. "author_id").
	// Each generator converts this to its own naming convention as needed.
	ViaFK string

	// RelationName matches the paired ForeignKey.RelationName, naming the relation
	// on the has-many side when a model pair has more than one relation. Empty
	// otherwise.
	RelationName string
}

// Index describes a multi-column index declared via protorm.v1.table.indexes.
type Index struct {
	Name    string   // explicit name; auto-generated when empty
	Columns []string // snake_case column names in declaration order
	Unique  bool
}

// ForeignKey describes a column-level FK constraint inferred from
// google.api.resource_reference. ReferencedColumn is resolved in a second pass
// after all tables in the database are built.
type ForeignKey struct {
	Column           string // referencing column in this table
	ReferencedSchema string // schema of the target table (e.g. "bookstore_v1")
	ReferencedTable  string // target table name (e.g. "authors")
	ReferencedModel  string // singular Go model name (e.g. "Author")
	ReferencedColumn string // actual PK column; "id" when table not found in database

	// ReferencedProto is the full proto message name of the target, set for
	// synthesized embed relations so resolution picks the exact table even when
	// several models share a simple name (e.g. a per-package "Media"). Empty for
	// resource_reference FKs, which resolve by model name.
	ReferencedProto string

	// OnDelete / OnUpdate are SQL referential actions ("CASCADE", "SET NULL", …)
	// from protorm.v1.col.on_delete / on_update. Empty means database default.
	OnDelete string
	OnUpdate string

	// RelationName disambiguates multiple relations between the same two models.
	// Prisma requires a matching @relation("name") on both the belongs-to and the
	// has-many side when more than one relation connects a model pair. Empty when
	// the pair has a single relation (no name needed). Mirrored onto the paired
	// HasManyRef.RelationName.
	RelationName string
}
