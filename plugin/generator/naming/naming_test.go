package naming

import "testing"

func TestPascalGo(t *testing.T) {
	cases := map[string]string{
		"author_id":    "AuthorID",
		"isbn":         "ISBN",
		"display_name": "DisplayName",
		"api_url":      "APIURL",
		"uuid":         "UUID",
		"pololu_12":    "Pololu12",
		"name":         "Name",
	}
	for in, want := range cases {
		if got := PascalGo(in); got != want {
			t.Errorf("PascalGo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamel(t *testing.T) {
	cases := map[string]string{
		"author_id":      "authorID",
		"display_name":   "displayName",
		"published_year": "publishedYear",
		"tag_ids":        "tagIDs",
		"name":           "name",
	}
	for in, want := range cases {
		if got := Camel(in); got != want {
			t.Errorf("Camel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCamelFirst(t *testing.T) {
	cases := map[string]string{"Author": "author", "BookAuthor": "bookAuthor", "": ""}
	for in, want := range cases {
		if got := CamelFirst(in); got != want {
			t.Errorf("CamelFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"Book":        "book",
		"BookAuthor":  "book_author",
		"AuthorID":    "author_id",   // trailing acronym
		"HTTPServer":  "http_server", // leading acronym run
		"ISBN":        "isbn",        // all-caps acronym
		"DisplayName": "display_name",
		"shelves":     "shelves", // already lower (google.api plural)
	}
	for in, want := range cases {
		if got := SnakeCase(in); got != want {
			t.Errorf("SnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnakePlural(t *testing.T) {
	cases := map[string]string{
		"Book":       "books",
		"BookAuthor": "book_authors",
		// Known limitation: naive +s; irregular plurals need table name overrides.
		"Shelf": "shelfs",
	}
	for in, want := range cases {
		if got := SnakePlural(in); got != want {
			t.Errorf("SnakePlural(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoPackage(t *testing.T) {
	cases := map[string]string{"bookstore_v1": "bookstorev1", "inventory": "inventory"}
	for in, want := range cases {
		if got := GoPackage(in); got != want {
			t.Errorf("GoPackage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDatasourceName(t *testing.T) {
	if got := DatasourceName("bookstore_db", "pgsql"); got != "bookstoredbpgsql" {
		t.Errorf("DatasourceName = %q, want bookstoredbpgsql", got)
	}
}

func TestEnumValueName(t *testing.T) {
	cases := []struct{ enum, value, want string }{
		{"Genre", "GENRE_FICTION", "FICTION"},
		{"Genre", "GENRE_NON_FICTION", "NON_FICTION"},
		{"Genre", "GENRE_UNSPECIFIED", "UNSPECIFIED"},
		{"Genre", "FICTION", "FICTION"},          // no prefix present
		{"ServoBoard", "SERVO_BOARD_CAN", "CAN"}, // multi-word enum name
	}
	for _, c := range cases {
		if got := EnumValueName(c.enum, c.value); got != c.want {
			t.Errorf("EnumValueName(%q, %q) = %q, want %q", c.enum, c.value, got, c.want)
		}
	}
}
