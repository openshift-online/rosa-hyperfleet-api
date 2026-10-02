package hyperfleetdb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/labels"
)

func parseLabelSelector(t *testing.T, selector string) labels.Selector {
	t.Helper()
	sel, err := labels.Parse(selector)
	require.NoError(t, err)
	return sel
}

func TestBuildLabelSelectorFilter(t *testing.T) {
	t.Run("equality uses JSONB containment", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "app=api"), 2)
		require.NoError(t, err)
		assert.Equal(t, []string{"(metadata->'labels' @> $2::jsonb)"}, clauses)
		assert.Equal(t, []any{json.RawMessage(`{"app":"api"}`)}, args)
	})

	t.Run("in uses ORed JSONB containment predicates", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "app in (api,web)"), 2)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"((metadata->'labels' @> $2::jsonb) OR (metadata->'labels' @> $3::jsonb))",
		}, clauses)
		assert.Equal(t, []any{
			json.RawMessage(`{"app":"api"}`),
			json.RawMessage(`{"app":"web"}`),
		}, args)
	})

	t.Run("notin includes objects with an absent key", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "app notin (api,web)"), 2)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"NOT COALESCE(((metadata->'labels' @> $2::jsonb) OR (metadata->'labels' @> $3::jsonb)), FALSE)",
		}, clauses)
		assert.Equal(t, []any{
			json.RawMessage(`{"app":"api"}`),
			json.RawMessage(`{"app":"web"}`),
		}, args)
	})

	t.Run("exists and does not exist", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "app,!legacy"), 4)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"COALESCE((metadata->'labels' ? $4), FALSE)",
			"NOT COALESCE((metadata->'labels' ? $5), FALSE)",
		}, clauses)
		assert.Equal(t, []any{"app", "legacy"}, args)
	})

	t.Run("greater than safely parses label values as int64", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "count>10"), 2)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"(CASE WHEN (metadata->'labels'->>$2) ~ '^[+-]?[0-9]+$' AND pg_input_is_valid((metadata->'labels'->>$2), 'bigint') THEN (metadata->'labels'->>$2)::bigint > $3::bigint ELSE FALSE END)",
		}, clauses)
		assert.Equal(t, []any{"count", "10"}, args)
	})

	t.Run("less than uses a strict numeric comparison", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(parseLabelSelector(t, "count<0"), 7)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"(CASE WHEN (metadata->'labels'->>$7) ~ '^[+-]?[0-9]+$' AND pg_input_is_valid((metadata->'labels'->>$7), 'bigint') THEN (metadata->'labels'->>$7)::bigint < $8::bigint ELSE FALSE END)",
		}, clauses)
		assert.Equal(t, []any{"count", "0"}, args)
	})

	t.Run("nothing selector", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(labels.Nothing(), 2)
		require.NoError(t, err)
		assert.Equal(t, []string{"FALSE"}, clauses)
		assert.Empty(t, args)
	})

	t.Run("empty selector", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(labels.Everything(), 2)
		require.NoError(t, err)
		assert.Empty(t, clauses)
		assert.Empty(t, args)
	})
}
