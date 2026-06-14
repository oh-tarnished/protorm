// Command protoc-gen-protorm is a protoc plugin that reads proto descriptors
// annotated with google.api.* and protorm.v1.* options, then generates
// database schema artifacts for the requested backend.
//
// # Install
//
//	go install github.com/oh-tarnished/protorm/plugin/cmd/protoc-gen-protorm@latest
//
// # Usage via buf.gen.yaml
//
//	plugins:
//	  - local: protoc-gen-protorm
//	    out: generated/
//	    opt:
//	      - target=prisma   # prisma | gorm | sql | csv
//
// # Inference priority
//
//  1. google.api.* annotations   — drives table, column, FK inference (80 %)
//  2. protorm.v1.* options       — overrides: type, name, skip, unique, index
//  3. buf.gen.yaml opt:          — global defaults (target backend)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/oh-tarnished/protorm/plugin/generator"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

// Build metadata, injected at release time via -ldflags "-X main.version=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// When invoked directly with -version (not by protoc), print and exit before
	// protogen tries to read a CodeGeneratorRequest from stdin.
	if len(os.Args) == 2 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Printf("protoc-gen-protorm %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	// flags are populated by protogen before the Run closure is called.
	// ParamFunc maps each "key=value" from buf.gen.yaml opt: to flags.Set.
	var flags flag.FlagSet

	target := flags.String(
		"target", "",
		"output backend: prisma | gorm | sql | csv",
	)
	strict := flags.Bool(
		"strict", false,
		"treat schema warnings (unresolved resource_references, unknown index columns) as errors",
	)

	protogen.Options{
		ParamFunc: flags.Set,
	}.Run(func(p *protogen.Plugin) error {
		// Proto3 `optional` is fully supported (it only affects field presence,
		// which protorm reads via field_behavior, not synthetic oneofs); declare
		// it so buf/protoc don't warn for files that use it.
		p.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		return generator.Generate(p, generator.Options{
			// *target/*strict are dereferenced inside the closure so that
			// ParamFunc has already populated them before we read the values.
			Target:  *target,
			Strict:  *strict,
			Version: version,
		})
	})
}
