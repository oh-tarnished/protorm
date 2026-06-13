{{.Header}}

CREATE SCHEMA IF NOT EXISTS {{.SchemaQ}};
{{range .Enums}}
-- {{.Comment}}
CREATE TYPE {{.TypeRef}} AS ENUM ({{.ValueList}});
{{end}}
{{- range .Tables}}
{{- if .Comment}}
-- {{.Comment}}
{{- end}}
CREATE TABLE {{.Ref}} (
{{- range .Items}}
{{- if .Comment}}
    -- {{.Comment}}
{{- end}}
    {{.Def}}{{if not .Last}},{{end}}
{{- end}}
);
{{range .Indexes}}{{.}}
{{end}}
{{- end}}