{{.Header}}

import { config } from "dotenv";
import { defineConfig, env } from "prisma/config";
import { resolve } from "path";

// Prisma 7 no longer auto-loads .env, so load it here before env() is read.
config({ path: resolve(__dirname, ".env") });

export default defineConfig({
	// All .prisma files in this folder and its subdirectories are loaded.
	schema: resolve(__dirname),
	migrations: {
		path: resolve(__dirname, "migrations"),
	},
	datasource: {
		// Single database URL for all schemas.
{{- if .URL}}
		// Declared in proto: {{.URL}}
{{- end}}
		url: env("{{.EnvVar}}"),
	},
});
