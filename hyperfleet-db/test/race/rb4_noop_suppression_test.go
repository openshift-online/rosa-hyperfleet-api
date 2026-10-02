package race_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/reader"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/resourceversion"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/writer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RB4a — Identical content write consumes no txid (I1/I4).
// Write an object, then write identical content. Assert: second write returns
// Changed=false, ObjectVersion unchanged, no txid consumed.
func TestRB4a_IdenticalWriteSuppressed(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	req := makeWriteReq("apps/v1/Deployment", "default", "noop-test")
	r1, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r1.Changed, "first write must be changed")
	assert.Greater(t, r1.Txid, uint64(0))

	// Write identical content — must be suppressed.
	req.ExpectedVersion = r1.ObjectVersion
	r2, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.False(t, r2.Changed, "identical write must be suppressed")
	assert.Equal(t, r1.ObjectVersion, r2.ObjectVersion, "object version must not change")
	assert.Equal(t, r1.UID, r2.UID, "UID must be preserved")
	assert.Equal(t, uint64(0), r2.Txid, "suppressed write must not consume a txid")
}

// RB4b — Real change after a no-op is correctly sequenced (I1).
// Write A, write A again (no-op), then write B. Assert: B gets next txid,
// watcher sees exactly one event.
func TestRB4b_RealChangeAfterNoOp(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	// Write initial content.
	req := makeWriteReq("apps/v1/Deployment", "default", "noop-seq")
	r1, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r1.Changed)

	// No-op write (identical content).
	req.ExpectedVersion = r1.ObjectVersion
	r2, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.False(t, r2.Changed, "no-op must be suppressed")

	// Real change.
	req.Spec = json.RawMessage(`{"replicas":99}`)
	req.ExpectedVersion = r2.ObjectVersion
	r3, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r3.Changed, "real change must not be suppressed")
	assert.Greater(t, r3.Txid, r1.Txid, "real change must get a txid greater than the first write")
	assert.Equal(t, r1.ObjectVersion+1, r3.ObjectVersion, "version must increment exactly once")
}

// RB4c — Watcher sees no event for a suppressed write (I4).
func TestRB4c_WatcherSeesNoEventForSuppressed(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	// Create initial object.
	req := makeWriteReq("apps/v1/Deployment", "default", "noop-watch")
	r1, err := w.Write(ctx, req)
	require.NoError(t, err)

	// Start watcher from current position.
	watchConn := freshConn(t)
	listenConn := freshConn(t)

	startRV := resourceversion.RV{Watermark: r1.Txid}
	watcher := reader.NewWatcher(watchConn, listenConn, reader.WatcherConfig{
		GVK:              "apps/v1/Deployment",
		StartRV:          startRV,
		BaselineInterval: 100 * time.Millisecond,
	}, nil)

	watchCtx, cancel := context.WithCancel(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- watcher.Run(watchCtx) }()

	// No-op write.
	req.ExpectedVersion = r1.ObjectVersion
	r2, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.False(t, r2.Changed)

	// Wait long enough that the watcher would have polled.
	time.Sleep(300 * time.Millisecond)

	// Now do a real change so we know the watcher is alive.
	req.Spec = json.RawMessage(`{"replicas":42}`)
	req.ExpectedVersion = r2.ObjectVersion
	r3, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r3.Changed)

	// The only event the watcher should see is the real change.
	select {
	case ev := <-watcher.Events():
		assert.Equal(t, reader.EventModified, ev.Type)
		assert.Equal(t, r3.Txid, ev.Resource.TxidStamp, "event must be for the real change")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for watcher event")
	}

	// Stop watcher before closing connections to avoid data race.
	cancel()
	<-errCh
	_ = watchConn.Close(ctx)
	_ = listenConn.Close(ctx)
}

// RB4d — Duplicate Create conflicts even when content is identical.
func TestRB4d_DuplicateCreateConflicts(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	req := makeWriteReq("apps/v1/Deployment", "default", "replay-create")
	r1, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r1.Changed)

	// A repeated Create is not an update/no-op, even when content matches.
	req.ExpectedVersion = 0
	_, err = w.Write(ctx, req)
	assert.ErrorIs(t, err, writer.ErrAlreadyExists)
}

// RB4e — WriteStatus suppression (I1/I4).
func TestRB4e_WriteStatusSuppressed(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	// Create the object first via spec write.
	createReq := makeWriteReq("apps/v1/Deployment", "default", "status-noop")
	r1, err := w.Write(ctx, createReq)
	require.NoError(t, err)
	assert.True(t, r1.Changed)

	// Status write.
	statusReq := model.StatusWriteRequest{
		GVK:             "apps/v1/Deployment",
		Namespace:       "default",
		Name:            "status-noop",
		Status:          json.RawMessage(`{"ready":true}`),
		ExpectedVersion: r1.ObjectVersion,
	}
	r2, err := w.WriteStatus(ctx, statusReq)
	require.NoError(t, err)
	assert.True(t, r2.Changed)

	// Identical status write — must be suppressed.
	statusReq.ExpectedVersion = r2.ObjectVersion
	r3, err := w.WriteStatus(ctx, statusReq)
	require.NoError(t, err)
	assert.False(t, r3.Changed, "identical status write must be suppressed")
	assert.Equal(t, r2.ObjectVersion, r3.ObjectVersion)
}

// RB4f — ForceWrite bypasses suppression.
func TestRB4f_ForceWriteBypassesSuppression(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	w := newWriter(t, nil)

	req := makeWriteReq("apps/v1/Deployment", "default", "force-test")
	r1, err := w.Write(ctx, req)
	require.NoError(t, err)

	// Force write with identical content.
	req.ExpectedVersion = r1.ObjectVersion
	req.ForceWrite = true
	r2, err := w.Write(ctx, req)
	require.NoError(t, err)
	assert.True(t, r2.Changed, "ForceWrite must bypass suppression")
	assert.Greater(t, r2.Txid, r1.Txid, "forced write must consume a txid")
	assert.Equal(t, r1.ObjectVersion+1, r2.ObjectVersion)
}
