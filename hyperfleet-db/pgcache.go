package hyperfleetdb

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/reader"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/resourceversion"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// pgCache implements cache.Cache backed by postgres list/watch.
type pgCache struct {
	scheme     *runtime.Scheme
	pool       *pgxpool.Pool
	restMapper meta.RESTMapper
	logger     logr.Logger
	shard      *reader.ShardSpec
	unsharded  map[schema.GroupVersionKind]bool

	mu        sync.Mutex
	informers map[schema.GroupVersionKind]*pgInformer
	started   bool
	ctx       context.Context
	stopCh    chan struct{}
}

// --- cache.Reader (Get/List delegate directly to postgres) ---

func (c *pgCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	gvk, err := resolveGVK(c.scheme, obj)
	if err != nil {
		return err
	}
	gvkStr := gvkToString(gvk)

	poolConn, err := c.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer poolConn.Release()

	var r model.Resource
	err = poolConn.Conn().QueryRow(ctx, `
		SELECT gvk, namespace, name, uid, txid_stamp::text::bigint,
		       object_version, spec, status, metadata,
		       deletion_timestamp, created_at, updated_at
		FROM kubernetes_resources
		WHERE gvk = $1 AND namespace = $2 AND name = $3`,
		gvkStr, key.Namespace, key.Name,
	).Scan(
		&r.GVK, &r.Namespace, &r.Name, &r.UID,
		&r.TxidStamp, &r.ObjectVersion, &r.Spec, &r.Status,
		&r.Metadata, &r.DeletionTimestamp, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return mapGetError(err, gvk, key.Name)
	}

	if isFullyDeleted(r) {
		return mapGetError(pgx.ErrNoRows, gvk, key.Name)
	}

	return populateObject(obj, r, gvk)
}

func (c *pgCache) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(&listOpts)
	}

	listGVK, err := resolveGVK(c.scheme, list)
	if err != nil {
		return err
	}
	itemGVK := itemGVKFromListGVK(listGVK)
	gvkStr := gvkToString(itemGVK)

	poolConn, err := c.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer poolConn.Release()

	filter, err := buildListFilter(listOpts)
	if err != nil {
		return err
	}
	if filter != nil && filter.Limit > 0 && filter.Limit < math.MaxInt64 {
		filter.Limit++
	}

	result, err := reader.List(ctx, poolConn.Conn(), gvkStr, filter)
	if err != nil {
		return err
	}

	hasMore := listOpts.Limit > 0 && int64(len(result.Resources)) > listOpts.Limit
	if hasMore {
		result.Resources = result.Resources[:listOpts.Limit]
	}

	var items []client.Object
	for _, r := range result.Resources {
		obj, err := resourceToObject(r, c.scheme)
		if err != nil {
			return err
		}
		if listOpts.LabelSelector != nil && !listOpts.LabelSelector.Matches(labelSet(obj.GetLabels())) {
			continue
		}
		items = append(items, obj)
	}

	if err := setListItems(list, items); err != nil {
		return err
	}
	list.SetResourceVersion(result.ResourceVersion.String())
	if hasMore {
		last := result.Resources[len(result.Resources)-1]
		incomingCT, _ := decodeContinue(listOpts.Continue)
		watermark := incomingCT.TxidStampMax
		if watermark == 0 {
			watermark = result.ResourceVersion.Watermark
		}
		list.SetContinue(encodeContinue(continueToken{
			TxidStamp:    last.TxidStamp,
			TxidStampMax: watermark,
		}))
	}
	return nil
}

// --- cache.Informers ---

func (c *pgCache) GetInformer(
	ctx context.Context, obj client.Object, opts ...cache.InformerGetOption,
) (cache.Informer, error) {
	gvk, err := resolveGVK(c.scheme, obj)
	if err != nil {
		return nil, err
	}
	return c.getOrCreateInformer(gvk)
}

func (c *pgCache) GetInformerForKind(
	ctx context.Context, gvk schema.GroupVersionKind, opts ...cache.InformerGetOption,
) (cache.Informer, error) {
	return c.getOrCreateInformer(gvk)
}

func (c *pgCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	return nil
}

func (c *pgCache) Start(ctx context.Context) error {
	c.mu.Lock()
	c.started = true
	c.ctx = ctx
	c.stopCh = make(chan struct{})
	informers := make([]*pgInformer, 0, len(c.informers))
	for _, inf := range c.informers {
		informers = append(informers, inf)
	}
	c.mu.Unlock()

	var wg sync.WaitGroup
	for _, inf := range informers {
		wg.Add(1)
		go func(inf *pgInformer) {
			defer wg.Done()
			inf.inner.Run(c.stopCh)
		}(inf)
	}

	<-ctx.Done()
	close(c.stopCh)
	wg.Wait()
	return nil
}

func (c *pgCache) WaitForCacheSync(ctx context.Context) bool {
	c.mu.Lock()
	syncFuncs := make([]toolscache.InformerSynced, 0, len(c.informers))
	for _, inf := range c.informers {
		syncFuncs = append(syncFuncs, inf.inner.HasSynced)
	}
	c.mu.Unlock()

	return toolscache.WaitForCacheSync(ctx.Done(), syncFuncs...)
}

func (c *pgCache) IndexField(
	ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc,
) error {
	return nil
}

func (c *pgCache) getOrCreateInformer(gvk schema.GroupVersionKind) (*pgInformer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if inf, ok := c.informers[gvk]; ok {
		return inf, nil
	}

	gvkStr := gvkToString(gvk)
	listGVK := schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	}

	var gvkShard *reader.ShardSpec
	if c.shard != nil && !c.unsharded[gvk] {
		gvkShard = c.shard
	}

	lw := &listWatchWithoutWatchListSemantics{&toolscache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			poolConn, err := c.pool.Acquire(ctx)
			if err != nil {
				return nil, fmt.Errorf("list connect: %w", err)
			}
			defer poolConn.Release()

			var filter *reader.ListFilter
			if gvkShard != nil {
				filter = gvkShard.ToListFilter()
			}
			result, err := reader.List(ctx, poolConn.Conn(), gvkStr, filter)
			if err != nil {
				return nil, fmt.Errorf("list: %w", err)
			}

			listObj, err := c.scheme.New(listGVK)
			if err != nil {
				return nil, fmt.Errorf("scheme.New(%v): %w", listGVK, err)
			}

			var items []client.Object
			for _, r := range result.Resources {
				obj, err := resourceToObject(r, c.scheme)
				if err != nil {
					return nil, err
				}
				items = append(items, obj)
			}

			oList, ok := listObj.(client.ObjectList)
			if !ok {
				return nil, fmt.Errorf("type %T does not implement client.ObjectList", listObj)
			}
			if err := setListItems(oList, items); err != nil {
				return nil, err
			}
			oList.SetResourceVersion(result.ResourceVersion.String())

			return listObj, nil
		},
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			startRV := resourceversion.RV{}
			if opts.ResourceVersion != "" {
				rv, err := resourceversion.Parse(opts.ResourceVersion)
				if err != nil {
					return nil, fmt.Errorf("parse resourceVersion %q: %w", opts.ResourceVersion, err)
				}
				startRV = rv
			}

			pollPoolConn, err := c.pool.Acquire(ctx)
			if err != nil {
				return nil, fmt.Errorf("watch poll connect: %w", err)
			}
			pollConn := pollPoolConn.Hijack()

			listenPoolConn, err := c.pool.Acquire(ctx)
			if err != nil {
				_ = pollConn.Close(ctx)
				return nil, fmt.Errorf("watch listen connect: %w", err)
			}
			listenConn := listenPoolConn.Hijack()

			w := reader.NewWatcher(pollConn, listenConn, reader.WatcherConfig{
				GVK:     gvkStr,
				StartRV: startRV,
				Shard:   gvkShard,
			}, nil)

			watchCtx, watchCancel := context.WithCancel(ctx)
			cleanup := func() {
				_ = pollConn.Close(context.Background())
				_ = listenConn.Close(context.Background())
				watchCancel()
			}

			return newPgWatcher(watchCtx, w, c.scheme, startRV, cleanup), nil
		},
	}}

	exampleObj, err := c.scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("scheme.New(%v): %w", gvk, err)
	}

	si := toolscache.NewSharedIndexInformerWithOptions(lw, exampleObj, toolscache.SharedIndexInformerOptions{
		ObjectDescription: gvk.Kind,
	})

	inf := &pgInformer{inner: si}
	c.informers[gvk] = inf

	if c.started && c.stopCh != nil {
		go si.Run(c.stopCh)
	}

	return inf, nil
}

// --- pgInformer delegates to toolscache.SharedIndexInformer ---

type pgInformer struct {
	inner toolscache.SharedIndexInformer
}

func (inf *pgInformer) AddEventHandler(
	handler toolscache.ResourceEventHandler,
) (toolscache.ResourceEventHandlerRegistration, error) {
	return inf.inner.AddEventHandler(handler)
}

func (inf *pgInformer) AddEventHandlerWithResyncPeriod(
	handler toolscache.ResourceEventHandler, resyncPeriod time.Duration,
) (toolscache.ResourceEventHandlerRegistration, error) {
	return inf.inner.AddEventHandlerWithResyncPeriod(handler, resyncPeriod)
}

func (inf *pgInformer) AddEventHandlerWithOptions(
	handler toolscache.ResourceEventHandler, opts toolscache.HandlerOptions,
) (toolscache.ResourceEventHandlerRegistration, error) {
	return inf.inner.AddEventHandlerWithOptions(handler, opts)
}

func (inf *pgInformer) RemoveEventHandler(handle toolscache.ResourceEventHandlerRegistration) error {
	return inf.inner.RemoveEventHandler(handle)
}

func (inf *pgInformer) AddIndexers(indexers toolscache.Indexers) error {
	return inf.inner.AddIndexers(indexers)
}

func (inf *pgInformer) HasSynced() bool {
	return inf.inner.HasSynced()
}

func (inf *pgInformer) HasSyncedChecker() toolscache.DoneChecker {
	return inf.inner.HasSyncedChecker()
}

func (inf *pgInformer) IsStopped() bool {
	return inf.inner.IsStopped()
}

// compile-time interface checks
var _ cache.Cache = (*pgCache)(nil)
var _ cache.Informer = (*pgInformer)(nil)
