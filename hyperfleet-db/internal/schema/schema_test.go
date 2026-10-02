package schema_test

import (
	"context"
	"slices"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/test/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateCreatesAllTables(t *testing.T) {
	if testing.Short() {
		t.Skip("requires postgres")
	}

	db := testinfra.StartPostgres(t)
	conn := db.Connect(t)
	ctx := context.Background()

	rows, err := conn.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	require.NoError(t, err)
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}
	require.NoError(t, rows.Err())

	slices.Sort(tables)
	expected := []string{
		"compaction_horizon",
		"kubernetes_resources",
	}
	assert.Equal(t, expected, tables)
}

func TestMigrateCreatesLabelsGINIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("requires postgres")
	}

	db := testinfra.StartPostgres(t)
	conn := db.Connect(t)

	var accessMethod string
	err := conn.QueryRow(context.Background(), `
		SELECT am.amname
		FROM pg_class idx
		JOIN pg_index i ON i.indexrelid = idx.oid
		JOIN pg_am am ON am.oid = idx.relam
		WHERE idx.relname = 'idx_resources_labels'
	`).Scan(&accessMethod)
	require.NoError(t, err)
	assert.Equal(t, "gin", accessMethod)
}
