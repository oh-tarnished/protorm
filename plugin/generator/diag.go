package generator

// diag.go collects non-fatal schema problems found while building the IR and
// decides how to surface them based on the --strict option.

import (
	"fmt"
	"os"
	"strings"
)

// diagnostics accumulates schema problems that are recoverable but suspicious —
// an unresolved resource_reference, an index naming a column that does not
// exist. By default each is surfaced as a warning on stderr and codegen
// proceeds with a best-effort fallback; under --strict they are promoted to a
// single hard error, so a typo fails the build instead of silently generating
// plausible-but-wrong output.
type diagnostics struct {
	warnings []string
}

// warnf records one problem.
func (d *diagnostics) warnf(format string, args ...any) {
	d.warnings = append(d.warnings, fmt.Sprintf(format, args...))
}

// resolve reports the accumulated problems: as a single aggregated error when
// strict, otherwise by printing each to stderr (which buf/protoc surface to the
// user) and returning nil so generation continues.
func (d *diagnostics) resolve(strict bool) error {
	if len(d.warnings) == 0 {
		return nil
	}
	if strict {
		return fmt.Errorf("protorm: --strict: %d schema problem(s):\n  - %s",
			len(d.warnings), strings.Join(d.warnings, "\n  - "))
	}
	for _, w := range d.warnings {
		fmt.Fprintln(os.Stderr, "protorm: warning: "+w)
	}
	return nil
}
