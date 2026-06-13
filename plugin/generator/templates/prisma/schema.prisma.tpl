{{.Header}}

datasource {{.Datasource}} {
  provider = "{{.Provider}}"
{{- if .MultiSchema}}
  schemas  = [{{.SchemaList}}]
{{- end}}
}

generator client {
  provider = "prisma-client"
  output   = "./generated/client"
}
