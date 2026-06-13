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

		for _, s := range db.Schemas {
			for _, domain := range domainsOf(s) {
				path := fmt.Sprintf("%s/%s/%s.%s.prisma",
					db.Name, s.Name, domain, provider.FragmentExt())
				ff := p.NewGeneratedFile(path, "")
				view := fragmentView(db, s, domain, provider)
				if err := templates.Render(ff, "fragment.prisma.tpl", view); err != nil {
					return fmt.Errorf("prisma: %s: %w", path, err)
				}
			}
		}
	}
	return nil
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
var scaffoldFiles = []struct{ name, tpl string }{
	{"package.json", "package.json.tpl"},
	{"tsconfig.json", "tsconfig.json.tpl"},
	{".env.example", "env.example.tpl"},
	{".gitignore", "gitignore.tpl"},
	{"README.md", "readme.md.tpl"},
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

// domainsOf lists the distinct source proto file base names contributing
// tables or enums to s, in deterministic order.
func domainsOf(s *schema.Schema) []string {
	seen := map[string]bool{}
	for _, t := range s.Tables {
		seen[t.SourceFile] = true
	}
	for _, e := range s.Enums {
		seen[e.SourceFile] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
