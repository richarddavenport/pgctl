package engine

import "testing"

func TestParseDotenv(t *testing.T) {
	in := []byte(`
# a comment
POSTGRES_HOST=mbp-postgres-qat-01.postgres.database.azure.com
POSTGRES_PORT=5432
export POSTGRES_USERNAME=mbpiadmin
POSTGRES_PASSWORD="s3cret with spaces"
QUOTED_SINGLE='value'
EMPTY=
NOT_AN_ASSIGNMENT
`)
	got := ParseDotenv(in)

	for key, want := range map[string]string{
		"POSTGRES_HOST":     "mbp-postgres-qat-01.postgres.database.azure.com",
		"POSTGRES_PORT":     "5432",
		"POSTGRES_USERNAME": "mbpiadmin",
		"POSTGRES_PASSWORD": "s3cret with spaces",
		"QUOTED_SINGLE":     "value",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, ok := got["NOT_AN_ASSIGNMENT"]; ok {
		t.Error("a line with no `=` became a key")
	}
	if v, ok := got.Get("EMPTY"); ok {
		t.Errorf("Get(EMPTY) = %q, true; an empty value should read as absent", v)
	}
}
