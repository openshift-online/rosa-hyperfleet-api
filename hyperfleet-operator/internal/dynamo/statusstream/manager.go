package statusstream

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	hd "github.com/rrp-bot/rosa-hyperfleet-kube-applier/hyperfleet-dynamo/dynamodb"
)

// OnChange is called with a documentID whenever the watcher detects that an
// item in a status table was added, modified, or deleted.
type OnChange = hd.OnChange

type watcherHandle struct {
	cancel context.CancelFunc
}

// Manager discovers ManagementCluster CRs and runs one Watcher per
// (MC, table-suffix) pair. It polls the MC list periodically to start
// watchers for new MCs and stop watchers for removed MCs.
//
// The Watcher runs indefinitely; it handles relist internally on its own
// RelistInterval ticker. The Manager only starts a new watcher when an MC
// appears and cancels it when the MC is removed.
type Manager struct {
	dbClient      *dynamodb.Client
	mcReader      client.Reader
	tableSuffixes []string
	onChange      OnChange
	opts          hd.Options
	logger        logr.Logger
}

// NewManager creates a Manager. Call Run to start it.
// opts configures the underlying DynamoDB watcher (PollInterval, RelistInterval,
// ShardCount). The Logger field of opts is overwritten with logger.
func NewManager(
	dbClient *dynamodb.Client,
	mcReader client.Reader,
	tableSuffixes []string,
	onChange OnChange,
	logger logr.Logger,
	opts hd.Options,
) *Manager {
	opts.Logger = logger
	return &Manager{
		dbClient:      dbClient,
		mcReader:      mcReader,
		tableSuffixes: tableSuffixes,
		onChange:      onChange,
		opts:          opts,
		logger:        logger,
	}
}

// Run blocks until ctx is cancelled. It immediately syncs watchers, then
// re-syncs on every interval tick.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	active := make(map[string]watcherHandle)

	defer func() {
		for _, h := range active {
			h.cancel()
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	m.syncWatchers(ctx, active)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.syncWatchers(ctx, active)
		}
	}
}

func (m *Manager) syncWatchers(ctx context.Context, active map[string]watcherHandle) {
	var list hyperfleetv1alpha1.ManagementClusterList
	if err := m.mcReader.List(ctx, &list); err != nil {
		m.logger.Error(err, "failed to list ManagementCluster CRs")
		return
	}

	// Build the desired set of watcher keys.
	desired := make(map[string]struct{}, len(list.Items)*len(m.tableSuffixes))
	for _, mc := range list.Items {
		for _, suffix := range m.tableSuffixes {
			desired[mc.Name+suffix] = struct{}{}
		}
	}

	// Stop watchers for MCs that no longer exist.
	for key, h := range active {
		if _, ok := desired[key]; !ok {
			m.logger.Info("stopping status watcher", "key", key)
			h.cancel()
			delete(active, key)
		}
	}

	// Start or restart watchers as needed.
	for _, mc := range list.Items {
		// Skip test MCs to avoid consuming stream capacity during testing.
		if strings.HasPrefix(mc.Name, "test-mc-") {
			continue
		}

		for _, suffix := range m.tableSuffixes {
			key := mc.Name + suffix

			if _, running := active[key]; running {
				// Already watching — nothing to do.
				continue
			}

			tableName := mc.Name + suffix
			w := hd.New(m.dbClient, tableName, m.onChange, m.opts)
			watcherCtx, cancel := context.WithCancel(ctx)
			active[key] = watcherHandle{cancel: cancel}
			m.logger.Info("starting status watcher", "mc", mc.Name, "table", tableName)
			go w.Run(watcherCtx)
		}
	}
}
