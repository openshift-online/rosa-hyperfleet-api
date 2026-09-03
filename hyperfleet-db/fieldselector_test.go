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
		{"metadata.labels", "metadata->>'labels'"},
		{"spec.color", "spec->>'color'"},
		{"spec.template.containers", "spec->'template'->>'containers'"},
		{"spec.a.b.c.d", "spec->'a'->'b'->'c'->>'d'"},
		{"status.phase", "status->>'phase'"},
		{"status.conditions.ready", "status->'conditions'->>'ready'"},
	}
	for _, tt := range valid {
		t.Run(tt.field, func(t *testing.T) {
			col, extraArgs, err := fieldPathToSQL(tt.field, 3)
			require.NoError(t, err)
			assert.Equal(t, tt.want, col)
			assert.Nil(t, extraArgs)
		})
	}

	// Label keys are parameterized: the key becomes $paramIdx and the value $paramIdx+1.
	labelCases := []struct {
		field      string
		wantCol    string
		wantKeyArg string
	}{
		{"metadata.labels.app", "metadata->'labels'->>$3", "app"},
		{"metadata.labels.hyperfleet.io/account-id", "metadata->'labels'->>$3", "hyperfleet.io/account-id"},
		{"metadata.labels.my-label", "metadata->'labels'->>$3", "my-label"},
	}
	for _, tt := range labelCases {
		t.Run(tt.field, func(t *testing.T) {
			col, extraArgs, err := fieldPathToSQL(tt.field, 3)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCol, col)
			require.Len(t, extraArgs, 1)
			assert.Equal(t, tt.wantKeyArg, extraArgs[0])
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
		{"invalid label key — empty name", "metadata.labels.", "invalid label key"},
		{"invalid label key — multiple slashes", "metadata.labels.foo/bar/baz", "invalid label key"},
		{"invalid label key — invalid DNS prefix", "metadata.labels.invalid..prefix/name", "invalid label key"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := fieldPathToSQL(tt.field, 3)
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

	t.Run("label field — key and value are separate bound parameters", func(t *testing.T) {
		clauses, args, err := buildFieldSelectorFilter(
			fields.SelectorFromSet(fields.Set{"metadata.labels.hyperfleet.io/account-id": "123456789012"}), 3)
		require.NoError(t, err)
		// Key is $3, value is $4 — no literal key in SQL text.
		assert.Equal(t, []string{"metadata->'labels'->>$3 = $4"}, clauses)
		assert.Equal(t, []any{"hyperfleet.io/account-id", "123456789012"}, args)
	})

	t.Run("label field — quote/comment payload in key is rejected by validation", func(t *testing.T) {
		// A K8s-invalid label key containing SQL metacharacters must be rejected
		// before reaching parameterization, verifying defense-in-depth.
		_, _, err := buildFieldSelectorFilter(
			fields.SelectorFromSet(fields.Set{"metadata.labels.'; DROP TABLE--": "x"}), 3)
		assert.ErrorContains(t, err, "invalid label key")
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
	t.Run("round trip with watermark", func(t *testing.T) {
		ct := continueToken{TxidStamp: 42, TxidStampMax: 99}
		decoded, err := decodeContinue(encodeContinue(ct))
		require.NoError(t, err)
		assert.Equal(t, uint64(42), decoded.TxidStamp)
		assert.Equal(t, uint64(99), decoded.TxidStampMax)
	})

	t.Run("empty", func(t *testing.T) {
		ct, err := decodeContinue("")
		require.NoError(t, err)
		assert.Equal(t, uint64(0), ct.TxidStamp)
		assert.Equal(t, uint64(0), ct.TxidStampMax)
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := decodeContinue("not-valid!!!")
		assert.ErrorIs(t, err, ErrInvalidContinueToken)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := decodeContinue("bm90LWpzb24=")
		assert.ErrorIs(t, err, ErrInvalidContinueToken)
	})
}
