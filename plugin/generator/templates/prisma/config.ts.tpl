{{.Header}}

import { resolve } from "node:path";
import { config } from "dotenv";
import { defineConfig, env } from "prisma/config";

// Prisma 7 no longer auto-loads .env, so load it here before env() is read.
config({ path: resolve(__dirname, ".env") });

export default defineConfig({
	// Point to the folder holding every .prisma file; subdirectories are
	// discovered automatically, so all schemas load from one config.
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
