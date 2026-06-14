// Package naming holds every identifier-case rule shared by the backends:
// snake_case ↔ camelCase ↔ PascalCase conversion, Go acronym handling, Go
// package sanitizing, and Prisma datasource naming.
package naming

import "strings"

// goAcronyms maps lowercase tokens to their canonical Go uppercase forms per
// https://github.com/golang/go/wiki/CodeReviewComments#initialisms.
var goAcronyms = map[string]string{
	"id": "ID", "url": "URL", "http": "HTTP", "https": "HTTPS",
	"uuid": "UUID", "api": "API", "uri": "URI", "ip": "IP",
	"isbn": "ISBN", "sql": "SQL", "db": "DB", "json": "JSON",
	"xml": "XML", "html": "HTML", "ok": "OK", "uid": "UID",
}

// PascalGo converts snake_case to PascalCase following Go acronym conventions.
// "author_id" → "AuthorID", "isbn" → "ISBN", "display_name" → "DisplayName".
func PascalGo(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "_") {
		if upper, ok := goAcronyms[strings.ToLower(part)]; ok {
			b.WriteString(upper)
		} else if len(part) > 0 {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

// Camel converts snake_case to camelCase with Prisma acronym conventions.
// Only "id"/"ids" segments after the first are uppercased.
// "author_id" → "authorID", "display_name" → "displayName", "name" → "name".
func Camel(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 0 {
		return s
	}
	var b strings.Builder
	b.WriteString(parts[0]) // first segment stays lowercase as-is
	for _, p := range parts[1:] {
		switch strings.ToLower(p) {
		case "id":
			b.WriteString("ID")
		case "ids":
			b.WriteString("IDs")
		default:
			if len(p) > 0 {
				b.WriteString(strings.ToUpper(p[:1]) + p[1:])
			}
		}
	}
	return b.String()
}

// CamelFirst lowercases the first rune of a PascalCase model name.
// "Author" → "author", "BookAuthor" → "bookAuthor".
func CamelFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// SnakeCase converts PascalCase to snake_case, keeping acronym runs intact:
// "BookAuthor" → "book_author", "AuthorID" → "author_id", "HTTPServer" →
// "http_server", "ISBN" → "isbn". A word boundary precedes an uppercase rune
// only when the previous rune is not uppercase (lower→Upper, e.g. "kA"), or
// when an uppercase run ends because the next rune is lowercase (the trailing
// capital starts a new word, e.g. the "S" in "HTTPServer").
func SnakeCase(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prevUpper := runes[i-1] >= 'A' && runes[i-1] <= 'Z'
			nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if !prevUpper || nextLower {
				b.WriteByte('_')
			}
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// SnakePlural converts PascalCase to snake_case + "s". "BookAuthor" → "book_authors".
func SnakePlural(s string) string { return SnakeCase(s) + "s" }

// GoPackage sanitizes a schema name into a valid idiomatic Go package name:
// lowercase, no underscores. "bookstore_v1" → "bookstorev1".
func GoPackage(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// DatasourceName builds a Prisma datasource identifier from the database name
// and provider suffix, matching the hand-written convention:
// ("bookstore_db", "pgsql") → "bookstoredbpgsql".
func DatasourceName(dbName, suffix string) string {
	return strings.ReplaceAll(dbName, "_", "") + suffix
}

// EnumValueName strips the enum-name prefix from a proto enum value, per the
// proto style guide ("GENRE_FICTION" with enum "Genre" → "FICTION").
func EnumValueName(enumName, valueName string) string {
	prefix := strings.ToUpper(SnakeCase(enumName)) + "_"
	if trimmed := strings.TrimPrefix(valueName, prefix); trimmed != "" {
		return trimmed
	}
	return valueName
}

// ScreamingSnake normalizes an identifier to SCREAMING_SNAKE_CASE: uppercase
// with `_` word separators. Already-SCREAMING_SNAKE input is preserved
// ("FOCUS_TIME" → "FOCUS_TIME"); camelCase/PascalCase boundaries become
// underscores ("focusTime" → "FOCUS_TIME"). Runs of underscores collapse to one.
// Used to keep generated enum values identical across the Prisma, GORM, and SQL
// targets (all read EnumValue.MapName).
func ScreamingSnake(s string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range s {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && prevLower {
			b.WriteByte('_')
		}
		b.WriteRune(r)
		prevLower = r >= 'a' && r <= 'z'
	}
	out := strings.ToUpper(b.String())
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return out
}

// SchemaDomain extracts the domain segment from a schema name for use as a
// disambiguating prefix on globally-namespaced Prisma model/enum names: the last
// underscore segment with any trailing API version dropped.
// "machanirobotics_app_productivity_calendar_v1" → "calendar";
// "store_apps_health_wellness_v1" → "wellness"; "public" → "public".
func SchemaDomain(schemaName string) string {
	parts := strings.Split(schemaName, "_")
	for len(parts) > 1 {
		last := parts[len(parts)-1]
		if len(last) >= 2 && last[0] == 'v' && last[1] >= '0' && last[1] <= '9' {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	if len(parts) == 0 {
		return schemaName
	}
	return parts[len(parts)-1]
}

// StripIDSuffix drops a trailing "_id" so a FK column yields a relation stem:
// "location_id" → "location", "organizer_user_app_ref_id" →
// "organizer_user_app_ref". A bare "id" (or a name without the suffix) is
// returned unchanged.
func StripIDSuffix(col string) string {
	if s := strings.TrimSuffix(col, "_id"); s != "" && s != col {
		return s
	}
	return col
}

// SanitizeIdent makes s a valid Prisma/SQL identifier by prefixing `_` when it
// would otherwise begin with a non-letter (e.g. a digit-leading enum value
// "9S" → "_9S"). The empty string maps to "_".
func SanitizeIdent(s string) string {
	if s == "" {
		return "_"
	}
	c := s[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_') {
		return "_" + s
	}
	return s
}
