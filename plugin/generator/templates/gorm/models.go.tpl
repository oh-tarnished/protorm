{{.Header}}

package {{.Package}}
{{if .Imports}}
import (
{{- range .Imports}}
	"{{.}}"
{{- end}}
)
{{end}}
{{- range .Enums}}
// {{.Comment}}
type {{.Name}} string

// {{.Name}} values as stored in the database.
const (
{{- range .Values}}
	{{.ConstName}} {{.TypeName}} = "{{.MapName}}"
{{- end}}
)
{{end}}
{{- range .Models}}
// {{.Comment}}
type {{.Name}} struct {
{{- range .Fields}}
{{- if .Comment}}
	// {{.Comment}}
{{- end}}
	{{.Decl}}
{{- end}}
}

func (*{{.Name}}) TableName() string { return "{{.TableName}}" }
{{end}}