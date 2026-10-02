package hyperfleetdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/fields"
)

func TestFieldPathToSQL(t *testing.T) {
	valid := []struct {
		field, want string
	}{
		{"metadata.name", "name"},
		{"metadata.namespace", "namespace"},
		{"metadata.uid", "uid::text"},
		{"metadata.labels", "metadata->>'labels'"},
		{"spec.color", "spec->>'color'"},
		{"spec.template.containers", "spec->'template'->>'containers'"},
		{"spec.a.b.c.d", "spec->'a'->'b'->'c'->>'d'"},
		{"status.phase", "status->>'phase'"},
		{"status.conditions.ready", "status->'conditions'->>'ready'"},
	}
	for _, tt := range valid {
		t.Run(tt.field, func(t *testing.T) {
			col, err := fieldPathToSQL(tt.field)
			require.NoError(t, err)
			assert.Equal(t, tt.want, col)
		})
	}

	invalid := []struct {
		name, field, errContains string
	}{
		{"no root", "name", "invalid field selector"},
		{"bad root", "labels.app", "unsupported field selector root"},
		{"SQL injection", "spec.foo'; DROP TABLE--", "invalid field selector path"},
		{"hyphen", "spec.my-field", "invalid field selector path"},
		{"starts with digit", "spec.1abc", "invalid field selector path"},
		{"empty segment", "spec.", "invalid field selector path"},
		{"special chars", "spec.foo$bar", "invalid field selector path"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fieldPathToSQL(tt.field)
			assert.ErrorContains(t, err, tt.errContains)
		})
	}
}

func TestBuildFieldSelectorFilter(t *testing.T) {
	t.Run("metadata.name equals", func(t *testing.T) {
		clauses, args, err := buildFieldSelectorFilter(fields.SelectorFromSet(fields.Set{"metadata.name": "my-pod"}), 3)
		require.NoError(t, err)
		assert.Equal(t, []string{"name = $3"}, clauses)
		assert.Equal(t, []any{"my-pod"}, args)
	})

	t.Run("spec field", func(t *testing.T) {
		clauses, args, err := buildFieldSelectorFilter(fields.SelectorFromSet(fields.Set{"spec.color": "blue"}), 3)
		require.NoError(t, err)
		assert.Equal(t, []string{"spec->>'color' = $3"}, clauses)
		assert.Equal(t, []any{"blue"}, args)
	})

	t.Run("multiple fields", func(t *testing.T) {
		clauses, args, err := buildFieldSelectorFilter(fields.SelectorFromSet(fields.Set{
			"metadata.namespace": "default",
			"spec.color":         "blue",
		}), 3)
		require.NoError(t, err)
		assert.Len(t, clauses, 2)
		assert.Len(t, args, 2)
	})

	t.Run("nil selector", func(t *testing.T) {
		clauses, _, err := buildFieldSelectorFilter(nil, 3)
		require.NoError(t, err)
		assert.Nil(t, clauses)
	})

	t.Run("empty selector", func(t *testing.T) {
		clauses, _, err := buildFieldSelectorFilter(fields.Everything(), 3)
		require.NoError(t, err)
		assert.Nil(t, clauses)
	})

	t.Run("invalid field", func(t *testing.T) {
		_, _, err := buildFieldSelectorFilter(fields.SelectorFromSet(fields.Set{"invalid": "value"}), 3)
		assert.Error(t, err)
	})
}

func TestContinueToken(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		offset, err := decodeContinue(encodeContinue(42))
		require.NoError(t, err)
		assert.Equal(t, int64(42), offset)
	})

	t.Run("empty", func(t *testing.T) {
		offset, err := decodeContinue("")
		require.NoError(t, err)
		assert.Equal(t, int64(0), offset)
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := decodeContinue("not-valid!!!")
		assert.ErrorContains(t, err, "invalid continue token")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := decodeContinue("bm90LWpzb24=")
		assert.ErrorContains(t, err, "invalid continue token")
	})
}
