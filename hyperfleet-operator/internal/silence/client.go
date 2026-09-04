package silence

import "context"

// Client manages Alertmanager silences.
type Client interface {
	List(ctx context.Context, identity ClusterIdentity) ([]GettableSilence, error)
	Create(ctx context.Context, silence PostableSilence) (string, error)
	Expire(ctx context.Context, id string) error
}
