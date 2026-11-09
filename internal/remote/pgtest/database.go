// Package pgtest provisions isolated PostgreSQL schemas for integration tests.
package pgtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// URL creates an isolated schema in WUU_TEST_DATABASE_URL and drops it on cleanup.
// Only use a disposable database whose role can create schemas.
func URL(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("WUU_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set WUU_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid WUU_TEST_DATABASE_URL")
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal("cannot connect to test PostgreSQL database")
	}
	schema := fmt.Sprintf("wuu_test_%x", rand.Text())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		conn.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
