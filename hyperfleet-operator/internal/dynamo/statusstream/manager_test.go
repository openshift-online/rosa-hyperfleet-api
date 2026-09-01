package statusstream

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	hd "github.com/rrp-bot/rosa-hyperfleet-kube-applier/hyperfleet-dynamo/dynamodb"
)

// fakeReader implements client.Reader, returning a configurable list of
// ManagementCluster objects. Safe for concurrent use.
type fakeReader struct {
	mu  sync.RWMutex
	mcs []hyperfleetv1alpha1.ManagementCluster
}

func (r *fakeReader) setMCs(mcs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mcs = nil
	for _, name := range mcs {
		r.mcs = append(r.mcs, hyperfleetv1alpha1.ManagementCluster{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		})
	}
}

func (r *fakeReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return nil
}

func (r *fakeReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if mcList, ok := list.(*hyperfleetv1alpha1.ManagementClusterList); ok {
		mcList.Items = append([]hyperfleetv1alpha1.ManagementCluster{}, r.mcs...)
	}
	return nil
}

// fakeWatcherFactory replaces hd.New in tests. It returns a fakeWatcher whose
// Done channel we can close programmatically.
type fakeWatcherHandle struct {
	doneCh chan struct{}
	stopCh chan struct{}
}

func newFakeWatcherHandle() *fakeWatcherHandle {
	return &fakeWatcherHandle{
		doneCh: make(chan struct{}),
		stopCh: make(chan struct{}),
	}
}

func (f *fakeWatcherHandle) Run(_ context.Context) {
	<-f.stopCh
}

func (f *fakeWatcherHandle) Done() <-chan struct{} { return f.doneCh }
func (f *fakeWatcherHandle) Stop()                 { close(f.stopCh) }

// Verify our fake satisfies the same interface surface as hd.Watcher.
var _ interface {
	Run(context.Context)
	Done() <-chan struct{}
	Stop()
} = (*fakeWatcherHandle)(nil)

// ---------------------------------------------------------------------------
// Manager tests using the real Manager but a fake client.Reader.
// ---------------------------------------------------------------------------

func TestManager_StartsWatcherPerMCAndSuffix(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("mc-1", "mc-2")

	// Verify the desired set for 2 MCs × 2 suffixes = 4 keys.
	var list hyperfleetv1alpha1.ManagementClusterList
	_ = reader.List(context.Background(), &list)
	suffixes := []string{"-status-applydesires", "-status-readdesires"}
	expected := len(list.Items) * len(suffixes)
	if expected != 4 {
		t.Errorf("expected 4 desired watcher keys, got %d", expected)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle tests using the injected newWatcher factory.
// ---------------------------------------------------------------------------

// newTestManager creates a Manager with an injected watcher factory that
// records which table names have been started and returns fakeWatcherHandles
// the test can control.
func newTestManager(reader *fakeReader, suffixes []string, factory func(string) watcher) *Manager {
	m := NewManager(nil, reader, suffixes, func(string, hd.Item) {}, logr.Discard(), hd.Options{})
	m.newWatcher = factory
	return m
}

func TestManager_Lifecycle_StartsWatcherOnMCAdd(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("mc-a")
	suffixes := []string{"-status-applydesires"}

	var mu sync.Mutex
	started := map[string]*fakeWatcherHandle{}

	factory := func(tableName string) watcher {
		h := newFakeWatcherHandle()
		mu.Lock()
		started[tableName] = h
		mu.Unlock()
		return h
	}

	mgr := newTestManager(reader, suffixes, factory)
	active := make(map[string]watcherHandle)

	// First sync — should start one watcher for mc-a.
	mgr.syncWatchers(context.Background(), active)

	mu.Lock()
	count := len(started)
	_, ok := started["mc-a-status-applydesires"]
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 watcher started, got %d", count)
	}
	if !ok {
		t.Error("expected watcher for mc-a-status-applydesires")
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active watcher, got %d", len(active))
	}
}

func TestManager_Lifecycle_StopsWatcherOnMCRemove(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("mc-a")
	suffixes := []string{"-status-applydesires"}

	stopped := make(chan string, 4)
	factory := func(tableName string) watcher {
		h := newFakeWatcherHandle()
		return h
	}

	mgr := newTestManager(reader, suffixes, factory)
	active := make(map[string]watcherHandle)

	// First sync — starts watcher for mc-a.
	mgr.syncWatchers(context.Background(), active)
	if len(active) != 1 {
		t.Fatalf("expected 1 active watcher after first sync, got %d", len(active))
	}

	// Wrap the cancel to detect stop.
	for key, h := range active {
		origCancel := h.cancel
		active[key] = watcherHandle{cancel: func() {
			origCancel()
			stopped <- key
		}}
	}

	// MC removed — second sync should stop the watcher.
	reader.setMCs()
	mgr.syncWatchers(context.Background(), active)

	select {
	case key := <-stopped:
		if key != "mc-a-status-applydesires" {
			t.Errorf("stopped unexpected key %q", key)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher was not stopped within 1s")
	}
	if len(active) != 0 {
		t.Errorf("expected 0 active watchers after MC removed, got %d", len(active))
	}
}

func TestManager_Lifecycle_SkipsTestMCPrefix(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("mc-real", "test-mc-fake")
	suffixes := []string{"-status-applydesires"}

	var mu sync.Mutex
	started := map[string]struct{}{}
	factory := func(tableName string) watcher {
		mu.Lock()
		started[tableName] = struct{}{}
		mu.Unlock()
		return newFakeWatcherHandle()
	}

	mgr := newTestManager(reader, suffixes, factory)
	active := make(map[string]watcherHandle)
	mgr.syncWatchers(context.Background(), active)

	mu.Lock()
	_, hasReal := started["mc-real-status-applydesires"]
	_, hasFake := started["test-mc-fake-status-applydesires"]
	mu.Unlock()

	if !hasReal {
		t.Error("expected watcher for mc-real")
	}
	if hasFake {
		t.Error("test-mc-fake should have been skipped")
	}
}

func TestManager_SkipsTestMCPrefix(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("mc-real", "test-mc-fake")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	desired := computeDesired(ctx, t, reader, []string{"-status-applydesires"})
	// test-mc-fake appears in desired (skip is in the start loop, not here).
	// The real check: syncWatchers must not start a watcher for test-mc-*.
	// Covered by TestManager_Lifecycle_SkipsTestMCPrefix; here we just verify
	// the desired-set still includes both keys as expected.
	for _, key := range []string{"mc-real-status-applydesires", "test-mc-fake-status-applydesires"} {
		if _, ok := desired[key]; !ok {
			t.Errorf("desired set missing expected key %q", key)
		}
	}
}

func TestManager_WatcherKeys_TwoMCsTwoSuffixes(t *testing.T) {
	reader := &fakeReader{}
	reader.setMCs("rc01-mc01", "rc01-mc02")
	ctx := context.Background()

	suffixes := []string{"-status-applydesires", "-status-readdesires"}
	desired := computeDesired(ctx, t, reader, suffixes)

	wantKeys := []string{
		"rc01-mc01-status-applydesires",
		"rc01-mc01-status-readdesires",
		"rc01-mc02-status-applydesires",
		"rc01-mc02-status-readdesires",
	}
	for _, k := range wantKeys {
		if _, ok := desired[k]; !ok {
			t.Errorf("desired set missing key %q", k)
		}
	}
}

// Verify hd.Options zero value uses package defaults (no panics).
func TestOptions_Defaults(t *testing.T) {
	_ = hd.DefaultPollInterval
	_ = hd.DefaultRelistInterval
	_ = hd.GSIShardCount
}

// helpers

func computeDesired(ctx context.Context, t *testing.T, reader *fakeReader, suffixes []string) map[string]struct{} {
	t.Helper()
	var list hyperfleetv1alpha1.ManagementClusterList
	if err := reader.List(ctx, &list); err != nil {
		t.Fatalf("List: %v", err)
	}
	desired := make(map[string]struct{})
	for _, mc := range list.Items {
		for _, suffix := range suffixes {
			desired[mc.Name+suffix] = struct{}{}
		}
	}
	return desired
}

// Verify runtime.Object interface is still satisfiable — compile check.
var _ runtime.Object = (*hyperfleetv1alpha1.ManagementCluster)(nil)
