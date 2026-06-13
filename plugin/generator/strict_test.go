package generator

// Strict-mode tests: schema problems that are recoverable warnings by default
// (codegen proceeds with a fallback) must become hard errors under Options.Strict.
// Fixtures live under testdata/strict/ (not testdata/cases/, so TestGolden
// ignores them) and are compiled with the same in-process harness as the golden
// tests.

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
)

func TestStrictMode(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		mentions string // substring the strict error must contain
	}{
		{"unresolved_fk", "testdata/strict/unresolved_fk", "Ghost"},
		{"bad_index", "testdata/strict/bad_index", "nonexistent_column"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Non-strict: the warning is tolerated and generation succeeds.
			if err := generateStrict(t, c.dir, false); err != nil {
				t.Fatalf("non-strict generate failed: %v", err)
			}
			// Strict: the same input fails with an error naming the problem.
			err := generateStrict(t, c.dir, true)
			if err == nil {
				t.Fatal("strict generate succeeded, want error")
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("strict error does not mention %q: %v", c.mentions, err)
			}
		})
	}
}

// generateStrict compiles the case protos and runs the sql target with the given
// strict setting, returning the generation error (if any).
func generateStrict(t *testing.T, dir string, strict bool) error {
	t.Helper()
	req := buildRequest(t, dir)
	p, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen: %v", err)
	}
	return Generate(p, Options{Target: "sql", Strict: strict})
}
