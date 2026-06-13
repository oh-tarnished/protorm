// Package generator is the orchestration layer of the protorm protoc plugin.
// It defines the backend registry, parses plugin options, builds the schema IR
// (see build.go), and dispatches to the chosen target.
//
// Adding a new backend:
//  1. Create plugin/generator/<name>/<name>.go implementing schema.Target.
//  2. Import it here and add it to the registry map.
package generator

import (
	"fmt"

	"github.com/oh-tarnished/protorm/plugin/generator/csv"
	"github.com/oh-tarnished/protorm/plugin/generator/gorm"
	"github.com/oh-tarnished/protorm/plugin/generator/prisma"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	sqlgen "github.com/oh-tarnished/protorm/plugin/generator/sql"
	"google.golang.org/protobuf/compiler/protogen"
)

// Options holds the plugin parameters supplied via buf.gen.yaml opt: entries.
//
// Example buf.gen.yaml snippet:
//
//	plugins:
//	  - local: protoc-gen-protorm
//	    out: generated/
//	    opt:
//	      - target=sql
type Options struct {
	// Target selects the output backend.
	// Accepted values: "prisma", "gorm", "sql", "csv".
	Target string

	// Strict promotes recoverable schema warnings (unresolved resource_references,
	// index columns that don't exist) into hard errors that fail generation.
	Strict bool

	// Version is the protoc-gen-protorm build version, written into the
	// generated-file banner. Empty renders as "(unknown)".
	Version string
}

// registry maps target names to their backend implementations.
// Keyed by the value users write in buf.gen.yaml opt: [target=<key>].
var registry = map[string]schema.Target{
	"prisma": &prisma.Generator{},
	"gorm":   &gorm.Generator{},
	"sql":    &sqlgen.Generator{},
	"csv":    &csv.Generator{},
}

// Generate is the single entry point called from the plugin binary.
//
// Flow:
//  1. Validate and resolve opts.Target against the registry.
//  2. Build the schema IR by traversing all generate-flagged proto files.
//  3. Hand the IR to the resolved target for rendering.
func Generate(p *protogen.Plugin, opts Options) error {
	if opts.Target == "" {
		return fmt.Errorf(
			"protorm: required option \"target\" is missing — " +
				"add opt: [target=prisma|gorm|sql|csv] to your buf.gen.yaml plugin entry",
		)
	}

	target, ok := registry[opts.Target]
	if !ok {
		return fmt.Errorf(
			"protorm: unknown target %q — valid targets: prisma, gorm, sql, csv",
			opts.Target,
		)
	}

	diags := &diagnostics{}
	dbs, err := buildDatabases(p, diags)
	if err != nil {
		return fmt.Errorf("protorm: schema inference failed: %w", err)
	}
	if err := diags.resolve(opts.Strict); err != nil {
		return err
	}

	protoc := protocVersion(p)
	for _, db := range dbs {
		db.PluginVersion = opts.Version
		db.ProtocVersion = protoc
	}

	return target.Generate(p, dbs)
}

// protocVersion formats the compiler version from the CodeGeneratorRequest the
// way protoc-gen-go does: "v<major>.<minor>.<patch>[-suffix]", or "(unknown)"
// when the invoker (e.g. the in-process test harness) supplies none.
func protocVersion(p *protogen.Plugin) string {
	v := p.Request.GetCompilerVersion()
	if v == nil {
		return ""
	}
	suffix := ""
	if s := v.GetSuffix(); s != "" {
		suffix = "-" + s
	}
	return fmt.Sprintf("v%d.%d.%d%s", v.GetMajor(), v.GetMinor(), v.GetPatch(), suffix)
}
