package hyperfleetdb_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestClient_UIDIsMintedByDatabase(t *testing.T) {
	mgr := newManager(t)
	c := mgr.GetClient()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "minted"}

	t.Run("create returns the uid", func(t *testing.T) {
		w := &Widget{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
		require.NoError(t, c.Create(ctx, w))
		require.NotEmpty(t, w.UID)

		got := &Widget{}
		require.NoError(t, c.Get(ctx, key, got))
		assert.Equal(t, w.UID, got.UID)
	})

	t.Run("client-sent uid is ignored", func(t *testing.T) {
		const sent = types.UID("00000000-0000-0000-0000-000000000001")
		w := &Widget{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "client-uid", UID: sent}}
		require.NoError(t, c.Create(ctx, w))
		assert.NotEqual(t, sent, w.UID)

		got := &Widget{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(w), got))
		assert.Equal(t, w.UID, got.UID)
	})

	t.Run("recreating a deleted name gives a new uid", func(t *testing.T) {
		first := &Widget{}
		require.NoError(t, c.Get(ctx, key, first))
		require.NoError(t, c.Delete(ctx, first))

		second := &Widget{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}}
		require.NoError(t, c.Create(ctx, second))
		assert.NotEqual(t, first.UID, second.UID)
	})

	t.Run("field selector on metadata.uid", func(t *testing.T) {
		w := &Widget{}
		require.NoError(t, c.Get(ctx, key, w))

		list := &WidgetList{}
		require.NoError(t, c.List(ctx, list, client.MatchingFields{"metadata.uid": string(w.UID)}))
		require.Len(t, list.Items, 1)
		assert.Equal(t, key.Name, list.Items[0].Name)

		require.NoError(t, c.List(ctx, list, client.MatchingFields{"metadata.uid": "not-a-uuid"}))
		assert.Empty(t, list.Items)
	})
}

func TestClient_ListWithLabelSelector(t *testing.T) {
	mgr := newManager(t)
	c := mgr.GetClient()
	ctx := context.Background()

	widgets := map[string]map[string]string{
		"none":     nil,
		"web-1":    {"app": "web", "tier": "1"},
		"web-3":    {"app": "web", "tier": "3"},
		"db-2":     {"app": "db", "tier": "2"},
		"cache-x":  {"app": "cache", "tier": "x"},
		"tierless": {"app": "web"},
	}
	for name, l := range widgets {
		require.NoError(t, c.Create(ctx, &Widget{ObjectMeta: metav1.ObjectMeta{
			Namespace: "default", Name: name, Labels: l,
		}}))
	}

	// Every operator must select exactly what labels.Selector.Matches selects.
	for _, expr := range []string{
		"app=web",
		"app==web",
		"app!=web",
		"app in (web,db)",
		"app notin (web,db)",
		"tier",
		"!tier",
		"tier>1",
		"tier<3",
		"app=web,tier",
		"app=web,tier>1",
		"app=missing",
	} {
		t.Run(expr, func(t *testing.T) {
			sel, err := labels.Parse(expr)
			require.NoError(t, err)

			var want []string
			for name, l := range widgets {
				if sel.Matches(labels.Set(l)) {
					want = append(want, name)
				}
			}

			list := &WidgetList{}
			require.NoError(t, c.List(ctx, list, client.MatchingLabelsSelector{Selector: sel}))
			var got []string
			for _, item := range list.Items {
				got = append(got, item.Name)
			}
			assert.ElementsMatch(t, want, got)
		})
	}

	t.Run("filters before limit", func(t *testing.T) {
		// Filtering in Go after LIMIT returned short or empty pages; in SQL
		// every page is full until the matches run out.
		sel := labels.SelectorFromSet(labels.Set{"app": "web"})
		var names []string
		list := &WidgetList{}
		opts := []client.ListOption{client.MatchingLabelsSelector{Selector: sel}, client.Limit(1)}
		for range 10 {
			require.NoError(t, c.List(ctx, list, opts...))
			require.Len(t, list.Items, 1)
			names = append(names, list.Items[0].Name)
			if len(names) == 3 {
				break
			}
			opts = append(opts[:2:2], client.Continue(list.Continue))
		}
		assert.ElementsMatch(t, []string{"web-1", "web-3", "tierless"}, names)
	})

	t.Run("cache list", func(t *testing.T) {
		list := &WidgetList{}
		require.NoError(t, mgr.GetCache().List(ctx, list, client.MatchingLabels{"app": "db"}))
		require.Len(t, list.Items, 1)
		assert.Equal(t, "db-2", list.Items[0].Name)
	})
}
