package schema

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Migrate(ctx context.Context, conn *pgx.Conn) error {
	files := []string{
		"migrations/001_initial.sql",
		"migrations/002_cluster_dns_uniqueness.sql",
		"migrations/003_oidcconfig_issuer_url_uniqueness.sql",
		"migrations/004_cluster_oidcconfig_uniqueness.sql",
	}
	for _, f := range files {
		sql, err := migrationsFS.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", f, err)
		}
	}
	return nil
}
