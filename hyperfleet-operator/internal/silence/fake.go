package silence

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// FakeClient is an in-memory Client for tests.
type FakeClient struct {
	mu         sync.Mutex
	silences   map[string]GettableSilence
	ExpireHook func(id string) error
}

func NewFakeClient() *FakeClient {
	return &FakeClient{silences: make(map[string]GettableSilence)}
}

func (f *FakeClient) List(_ context.Context, identity ClusterIdentity) ([]GettableSilence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []GettableSilence
	for _, s := range f.silences {
		if s.CreatedBy != CreatedBy || s.Status.State != "active" {
			continue
		}
		if silenceMatchesIdentity(s, identity) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *FakeClient) Create(_ context.Context, silence PostableSilence) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.NewString()
	now := time.Now().UTC()
	f.silences[id] = GettableSilence{
		ID:        id,
		Status:    SilenceStatus{State: "active"},
		UpdatedAt: now,
		Matchers:  append([]Matcher(nil), silence.Matchers...),
		StartsAt:  silence.StartsAt,
		EndsAt:    silence.EndsAt,
		CreatedBy: silence.CreatedBy,
		Comment:   silence.Comment,
	}
	return id, nil
}

func (f *FakeClient) Expire(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ExpireHook != nil {
		if err := f.ExpireHook(id); err != nil {
			return err
		}
	}
	s, ok := f.silences[id]
	if !ok {
		return fmt.Errorf("silence %q not found", id)
	}
	s.Status.State = "expired"
	f.silences[id] = s
	return nil
}

func silenceMatchesIdentity(s GettableSilence, identity ClusterIdentity) bool {
	has := map[string]string{}
	for _, m := range s.Matchers {
		if m.IsEqual && !m.IsRegex {
			has[m.Name] = m.Value
		}
	}
	return has["namespace"] == identity.Namespace &&
		has["name"] == identity.Name
}
