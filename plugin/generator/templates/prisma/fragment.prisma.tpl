{{.Header}}
{{range .Enums}}
/// {{.Comment}}
enum {{.Name}} {
{{- range .Values}}
  /// {{.Comment}}
  {{.Name}} @map("{{.MapName}}")
{{end}}
  /// Maps the enum to the "{{.SQLName}}" type in the database.
  @@map("{{.SQLName}}")
{{- if $.MultiSchema}}
  /// Maps the enum to the "{{$.Schema}}" schema in the database.
  @@schema("{{$.Schema}}")
{{- end}}
}
{{end}}
{{- range .Models}}
/// {{.Comment}}
model {{.Name}} {
{{- range .Fields}}
  /// {{.Doc}}
  {{.Decl}}
{{end}}
{{- range .Indexes}}
  /// {{.Doc}}
  {{.Decl}}
{{- end}}
  /// Maps the model to the "{{.Map}}" table in the database.
  @@map("{{.Map}}")
{{- if $.MultiSchema}}
  /// Maps the model to the "{{$.Schema}}" schema in the database.
  @@schema("{{$.Schema}}")
{{- end}}
}
{{end}}