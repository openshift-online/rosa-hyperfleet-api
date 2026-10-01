package hyperfleetdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/labels"
)

func TestBuildLabelSelectorFilter(t *testing.T) {
	tests := []struct {
		selector    string
		wantClauses []string
		wantArgs    []any
	}{
		{"app=web", []string{"metadata->'labels' @> $3::jsonb"}, []any{`{"app":"web"}`}},
		{"app!=web", []string{"metadata->'labels'->>$3 IS DISTINCT FROM $4"}, []any{"app", "web"}},
		{"app in (a,b)", []string{"metadata->'labels'->>$3 = ANY($4::text[])"}, []any{"app", []string{"a", "b"}}},
		{
			"app notin (a)",
			[]string{"(metadata->'labels'->>$3 IS NULL OR NOT metadata->'labels'->>$3 = ANY($4::text[]))"},
			[]any{"app", []string{"a"}},
		},
		{"app", []string{"metadata->'labels' ? $3"}, []any{"app"}},
		{"!app", []string{"NOT COALESCE(metadata->'labels' ? $3, false)"}, []any{"app"}},
		{
			"tier>2",
			[]string{"(CASE WHEN metadata->'labels'->>$3 ~ '^-?[0-9]{1,18}$' THEN (metadata->'labels'->>$3)::bigint > $4 ELSE false END)"},
			[]any{"tier", int64(2)},
		},
		{
			"app=web,tier",
			[]string{"metadata->'labels' @> $3::jsonb", "metadata->'labels' ? $4"},
			[]any{`{"app":"web"}`, "tier"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.selector, func(t *testing.T) {
			sel, err := labels.Parse(tt.selector)
			require.NoError(t, err)
			clauses, args, err := buildLabelSelectorFilter(sel, 3)
			require.NoError(t, err)
			assert.Equal(t, tt.wantClauses, clauses)
			assert.Equal(t, tt.wantArgs, args)
		})
	}

	t.Run("everything", func(t *testing.T) {
		clauses, args, err := buildLabelSelectorFilter(labels.Everything(), 3)
		require.NoError(t, err)
		assert.Empty(t, clauses)
		assert.Empty(t, args)
	})

	t.Run("nothing", func(t *testing.T) {
		clauses, _, err := buildLabelSelectorFilter(labels.Nothing(), 3)
		require.NoError(t, err)
		assert.Equal(t, []string{"false"}, clauses)
	})
}
