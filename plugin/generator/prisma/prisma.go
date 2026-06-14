// Package prisma generates a multi-file Prisma schema tree from the protorm IR,
// replicating the hand-written layout this repository uses:
//
//	<db>/schema.prisma                       — datasource + generator blocks
//	<db>/<schema>/<domain>.<provider>.prisma — models + enums per source proto file
//
// "domain" is the proto file base name, so bookstore/v1/bookstore.proto with
// schema bookstore_v1 renders bookstore_db/bookstore_v1/bookstore.postgres.prisma.
package prisma

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"

	"github.com/oh-tarnished/protorm/plugin/generator/header"
	"github.com/oh-tarnished/protorm/plugin/generator/naming"
	"github.com/oh-tarnished/protorm/plugin/generator/schema"
	"github.com/oh-tarnished/protorm/plugin/generator/templates"
	"github.com/oh-tarnished/protorm/plugin/generator/types"
)

// Generator implements schema.Target for Prisma schema output.
type Generator struct{}

// Name returns the target identifier used in buf.gen.yaml opt: [target=prisma].
func (g *Generator) Name() string { return "prisma" }

// Generate writes the datasource file and every per-domain fragment for each database.
func (g *Generator) Generate(p *protogen.Plugin, dbs []*schema.Database) error {
	for _, db := range dbs {
		provider := types.Provider(db.Provider)

		f := p.NewGeneratedFile(db.Name+"/schema.prisma", "")
		if err := templates.Render(f, "schema.prisma.tpl", schemaFileView(db, provider)); err != nil {
			return fmt.Errorf("prisma: %s: %w", db.Name, err)
		}

		// Prisma 7: the connection URL lives in <db>.config.ts, not the schema file.
		cf := p.NewGeneratedFile(db.Name+"/"+db.Name+".config.ts", "")
		if err := templates.Render(cf, "config.ts.tpl", configView(db)); err != nil {
			return fmt.Errorf("prisma: %s config: %w", db.Name, err)
		}

		// Project scaffold so the generated folder is a runnable Prisma project.
		if err := writeScaffold(p, db, provider); err != nil {
			return err
		}

		// One fragment per source proto file, placed at a path mirroring the
		// proto directory tree so the Prisma layout matches the protobuf layout.
		// A single proto may contribute models to several @@schemas, so each
		// model/enum carries its own schema rather than the fragment as a whole.
		groups := groupByProto(db)
		for _, g := range groups {
			dir := fragmentDir(db.Name, g.sourceDir, g.fileBase)
			path := fmt.Sprintf("%s/%s.%s.prisma", dir, g.fileBase, provider.FragmentExt())
			ff := p.NewGeneratedFile(path, "")
			if err := templates.Render(ff, "fragment.prisma.tpl", fragmentView(db, g, provider)); err != nil {
				return fmt.Errorf("prisma: %s: %w", path, err)
			}
		}

		// A README.md with a Mermaid ER diagram in every folder of the tree.
		if err := writeReadmes(p, db, groups, provider); err != nil {
			return err
		}
	}
	return nil
}

// fragmentGroup is one source proto file's tables and enums, gathered across
// every schema they were sorted into.
type fragmentGroup struct {
	sourceProto string
	sourceDir   string
	fileBase    string
	tables      []*schema.Table
	enums       []*schema.Enum
}

// groupByProto buckets a database's tables and enums by their source proto file,
// in deterministic order, so each proto renders to exactly one fragment file.
func groupByProto(db *schema.Database) []fragmentGroup {
	idx := map[string]*fragmentGroup{}
	var order []string
	get := func(proto, dir, base string) *fragmentGroup {
		g, ok := idx[proto]
		if !ok {
			g = &fragmentGroup{sourceProto: proto, sourceDir: dir, fileBase: base}
			idx[proto] = g
			order = append(order, proto)
		}
		return g
	}
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			g := get(t.SourceProto, t.SourceDir, t.SourceFile)
			g.tables = append(g.tables, t)
		}
		for _, e := range s.Enums {
			g := get(e.SourceProto, e.SourceDir, e.SourceFile)
			g.enums = append(g.enums, e)
		}
	}
	sort.Strings(order)
	out := make([]fragmentGroup, 0, len(order))
	for _, p := range order {
		out = append(out, *idx[p])
	}
	return out
}

// fragmentDir mirrors the proto directory under the database root. The proto
// tree's leading segment (the module/service root, e.g. "store") is dropped —
// the database name already stands in for it — and the file base name becomes a
// leaf folder so each proto gets its own directory (with room for a README):
//
//	db "users", dir "store/apps/productivity/calendar", base "event"
//	  → "users/apps/productivity/calendar/event"
func fragmentDir(dbName, sourceDir, fileBase string) string {
	rest := ""
	if i := strings.IndexByte(sourceDir, '/'); i >= 0 {
		rest = sourceDir[i+1:]
	}
	parts := []string{dbName}
	if rest != "" {
		parts = append(parts, rest)
	}
	parts = append(parts, fileBase)
	return strings.Join(parts, "/")
}

// schemaFileView prepares the datasource template data for one database.
func schemaFileView(db *schema.Database, provider types.Provider) map[string]any {
	names := make([]string, 0, len(db.Schemas))
	quoted := make([]string, 0, len(db.Schemas))
	for _, s := range db.Schemas {
		names = append(names, s.Name)
		quoted = append(quoted, `"`+s.Name+`"`)
	}
	suffix := "pgsql"
	if provider == types.MongoDB {
		suffix = "mongo"
	}
	return map[string]any{
		"Header": header.Render("//", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Database:      db.Name,
			SchemaLabel:   "schemas",
			Schema:        strings.Join(names, ", "),
			Notes:         []string{"Connection URLs live in " + db.Name + ".config.ts (Prisma 7 convention)."},
		}),
		"Datasource":  naming.DatasourceName(db.Name, suffix),
		"Provider":    provider.PrismaProvider(),
		"SchemaList":  strings.Join(quoted, ", "),
		"MultiSchema": provider == types.Postgres,
	}
}

// configView prepares the <db>.config.ts template data: the env var carrying
// the connection URL ("bookstore_db" → "BOOKSTORE_DB_DATABASE_URL").
func configView(db *schema.Database) map[string]any {
	names := make([]string, 0, len(db.Schemas))
	for _, s := range db.Schemas {
		names = append(names, s.Name)
	}
	return map[string]any{
		"Header": header.Render("//", header.Info{
			PluginVersion: db.PluginVersion,
			ProtocVersion: db.ProtocVersion,
			Database:      db.Name,
			SchemaLabel:   "schemas",
			Schema:        strings.Join(names, ", "),
			Notes:         []string{"Prisma 7 configuration; connection URLs are environment-driven."},
		}),
		"URL":    db.URL,
		"EnvVar": envVar(db),
	}
}

// scaffoldFiles maps each scaffold output path suffix to its template name.
// scaffoldFiles are the static project files; the README.md tree is generated
// separately by writeReadmes (one per folder, with a Mermaid ER diagram).
var scaffoldFiles = []struct{ name, tpl string }{
	{"package.json", "package.json.tpl"},
	{"tsconfig.json", "tsconfig.json.tpl"},
	{".env.example", "env.example.tpl"},
	{".gitignore", "gitignore.tpl"},
}

// writeScaffold emits the package.json, tsconfig.json, .env.example, .gitignore,
// and README.md that turn the database folder into a runnable Prisma project.
func writeScaffold(p *protogen.Plugin, db *schema.Database, provider types.Provider) error {
	view := scaffoldView(db, provider)
	for _, sf := range scaffoldFiles {
		f := p.NewGeneratedFile(db.Name+"/"+sf.name, "")
		if err := templates.Render(f, sf.tpl, view); err != nil {
			return fmt.Errorf("prisma: %s/%s: %w", db.Name, sf.name, err)
		}
	}
	return nil
}

// scaffoldView prepares the shared template data for the project scaffold files.
func scaffoldView(db *schema.Database, provider types.Provider) map[string]any {
	return map[string]any{
		"Database":    db.Name,
		"PackageName": strings.ReplaceAll(db.Name, "_", "-") + "-prisma",
		"EnvVar":      envVar(db),
		"URLExample":  exampleURL(db, provider),
		"ProviderExt": provider.FragmentExt(),
	}
}

// envVar derives the connection-URL environment variable name for a database.
func envVar(db *schema.Database) string {
	return strings.ToUpper(db.Name) + "_DATABASE_URL"
}

// exampleURL returns the connection URL written into .env.example: the one
// declared in the proto when present, otherwise a provider-appropriate stub.
func exampleURL(db *schema.Database, provider types.Provider) string {
	if db.URL != "" {
		return db.URL
	}
	if provider == types.MongoDB {
		return "mongodb://localhost:27017/" + db.Name
	}
	return "postgresql://user:password@localhost:5432/" + db.Name
}
