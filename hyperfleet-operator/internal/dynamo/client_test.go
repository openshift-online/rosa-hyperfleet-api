package dynamo

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// spyDB implements dynamoAPI in-memory for unit tests.
type spyDB struct {
	getCount int
	putCount int
	delCount int
	items    map[string]map[string]dynamodbtypes.AttributeValue // table/docID → item
}

func newSpyDB() *spyDB {
	return &spyDB{items: make(map[string]map[string]dynamodbtypes.AttributeValue)}
}

func (s *spyDB) key(table, docID string) string { return table + "/" + docID }

func (s *spyDB) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	s.getCount++
	docID := input.Key[attributeDocumentID].(*dynamodbtypes.AttributeValueMemberS).Value
	item, ok := s.items[s.key(*input.TableName, docID)]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (s *spyDB) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	s.putCount++
	docID := input.Item[attributeDocumentID].(*dynamodbtypes.AttributeValueMemberS).Value
	s.items[s.key(*input.TableName, docID)] = input.Item
	return &dynamodb.PutItemOutput{}, nil
}

func (s *spyDB) DeleteItem(_ context.Context, input *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	s.delCount++
	docID := input.Key[attributeDocumentID].(*dynamodbtypes.AttributeValueMemberS).Value
	delete(s.items, s.key(*input.TableName, docID))
	return &dynamodb.DeleteItemOutput{}, nil
}

// spySNSPublisher records SNS publish calls for assertion.
type spySNSPublisher struct {
	calls []snsPublishCall
}

type snsPublishCall struct {
	mcName      string
	documentID  string
	tableSuffix string
}

func (s *spySNSPublisher) Publish(_ context.Context, mcName, documentID, tableSuffix string) error {
	s.calls = append(s.calls, snsPublishCall{mcName: mcName, documentID: documentID, tableSuffix: tableSuffix})
	return nil
}

func newTestClient() (*Client, *spyDB) {
	spy := newSpyDB()
	c := NewClient(spy)
	return c, spy
}

func newTestClientWithSNS() (*Client, *spyDB, *spySNSPublisher) {
	spy := newSpyDB()
	snsSpy := &spySNSPublisher{}
	c := NewClientWithSNS(spy, snsSpy)
	return c, spy, snsSpy
}

func TestUpsertCacheHit(t *testing.T) {
	c, spy := newTestClient()
	ctx := context.Background()
	table := "test-applydesires"
	docID := "doc-1"
	spec := map[string]string{"key": "value"}

	// First upsert: cache miss → GetItem + PutItem.
	res, err := c.upsertDesire(ctx, table, docID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true on first upsert")
	}
	if spy.getCount != 1 || spy.putCount != 1 {
		t.Fatalf("first upsert: got get=%d put=%d, want get=1 put=1", spy.getCount, spy.putCount)
	}

	// Second upsert with same spec: cache hit → no DynamoDB calls.
	res, err = c.upsertDesire(ctx, table, docID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatal("expected Changed=false on cache hit")
	}
	if spy.getCount != 1 || spy.putCount != 1 {
		t.Fatalf("cache hit: got get=%d put=%d, want get=1 put=1", spy.getCount, spy.putCount)
	}
}

func TestUpsertCacheChangedSpec(t *testing.T) {
	c, spy := newTestClient()
	ctx := context.Background()
	table := "test-applydesires"
	docID := "doc-1"

	// First upsert.
	if _, err := c.upsertDesire(ctx, table, docID, map[string]string{"v": "1"}); err != nil {
		t.Fatal(err)
	}

	// Second upsert with different spec: cache miss on hash → GetItem + PutItem.
	res, err := c.upsertDesire(ctx, table, docID, map[string]string{"v": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true when spec changes")
	}
	if spy.getCount != 2 || spy.putCount != 2 {
		t.Fatalf("changed spec: got get=%d put=%d, want get=2 put=2", spy.getCount, spy.putCount)
	}

	// Third upsert with same v2 spec: cache hit.
	res, err = c.upsertDesire(ctx, table, docID, map[string]string{"v": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatal("expected Changed=false after cache updated with new spec")
	}
	if spy.getCount != 2 || spy.putCount != 2 {
		t.Fatalf("after update cache hit: got get=%d put=%d, want get=2 put=2", spy.getCount, spy.putCount)
	}
}

func TestUpsertCacheColdStart(t *testing.T) {
	c, spy := newTestClient()
	ctx := context.Background()
	table := "test-applydesires"
	docID := "doc-1"
	spec := map[string]string{"key": "value"}

	// Pre-populate the spy with an existing item (simulates restart — item exists in DynamoDB but not in cache).
	hash, _ := computeSpecHash(spec)
	existingTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	spy.items[spy.key(table, docID)] = map[string]dynamodbtypes.AttributeValue{
		attributeDocumentID: &dynamodbtypes.AttributeValueMemberS{Value: docID},
		"specHash":          &dynamodbtypes.AttributeValueMemberS{Value: hash},
		"updateTime":        &dynamodbtypes.AttributeValueMemberS{Value: existingTime.Format(time.RFC3339)},
	}

	// First upsert after restart: cache miss → GetItem (hash matches) → no PutItem, cache populated.
	res, err := c.upsertDesire(ctx, table, docID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Fatal("expected Changed=false when DynamoDB hash matches")
	}
	if spy.getCount != 1 || spy.putCount != 0 {
		t.Fatalf("cold start: got get=%d put=%d, want get=1 put=0", spy.getCount, spy.putCount)
	}

	// Second upsert: cache now warm → no DynamoDB calls.
	res, err = c.upsertDesire(ctx, table, docID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if spy.getCount != 1 || spy.putCount != 0 {
		t.Fatalf("warm cache: got get=%d put=%d, want get=1 put=0", spy.getCount, spy.putCount)
	}
}

func TestDeleteClearsCache(t *testing.T) {
	c, spy := newTestClient()
	ctx := context.Background()
	table := "test-applydesires"
	docID := "doc-1"
	spec := map[string]string{"key": "value"}

	// Populate cache via upsert.
	if _, err := c.upsertDesire(ctx, table, docID, spec); err != nil {
		t.Fatal(err)
	}

	// Verify cache is populated.
	ck := c.cacheKey(table, docID)
	if _, ok := c.cache.Load(ck); !ok {
		t.Fatal("expected cache entry after upsert")
	}

	// Delete clears cache.
	if err := c.DeleteDesireSpec(ctx, table, "", docID); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.cache.Load(ck); ok {
		t.Fatal("expected cache entry cleared after delete")
	}

	// Next upsert must go to DynamoDB again.
	spy.getCount = 0
	spy.putCount = 0
	res, err := c.upsertDesire(ctx, table, docID, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true after cache cleared")
	}
	if spy.getCount != 1 || spy.putCount != 1 {
		t.Fatalf("after delete: got get=%d put=%d, want get=1 put=1", spy.getCount, spy.putCount)
	}
}

func TestComputeSpecHash(t *testing.T) {
	h1, err := computeSpecHash(map[string]string{"a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := computeSpecHash(map[string]string{"a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	h3, err := computeSpecHash(map[string]string{"a": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("same input produced different hashes: %s != %s", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different input produced same hash: %s", h1)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(h1))
	}
}

// TestSNSPublishedOnChangedUpsert verifies that SNS is notified when a spec
// changes and not notified on cache hits (unchanged specs).
func TestSNSPublishedOnChangedUpsert(t *testing.T) {
	c, _, snsSpy := newTestClientWithSNS()
	ctx := context.Background()

	desire := &ApplyDesire{}
	desire.DocumentID = "doc-1"
	desire.Spec = ApplyDesireSpec{
		ManagementCluster: "prod",
		ClusterID:         "c1",
	}
	specsPrefix := "mc-prod-specs"

	// First upsert: spec is new → should publish.
	_, err := c.UpsertApplyDesire(ctx, specsPrefix, desire)
	if err != nil {
		t.Fatalf("UpsertApplyDesire: %v", err)
	}
	if len(snsSpy.calls) != 1 {
		t.Fatalf("expected 1 SNS call after new spec, got %d", len(snsSpy.calls))
	}
	if snsSpy.calls[0].mcName != "prod" {
		t.Errorf("mcName = %q, want %q", snsSpy.calls[0].mcName, "prod")
	}
	if snsSpy.calls[0].documentID != "doc-1" {
		t.Errorf("documentID = %q, want %q", snsSpy.calls[0].documentID, "doc-1")
	}
	if snsSpy.calls[0].tableSuffix != TableSuffixApplyDesires {
		t.Errorf("tableSuffix = %q, want %q", snsSpy.calls[0].tableSuffix, TableSuffixApplyDesires)
	}

	// Second upsert: spec unchanged → cache hit → no SNS call.
	_, err = c.UpsertApplyDesire(ctx, specsPrefix, desire)
	if err != nil {
		t.Fatalf("UpsertApplyDesire (unchanged): %v", err)
	}
	if len(snsSpy.calls) != 1 {
		t.Errorf("expected no additional SNS call on unchanged spec, got %d total", len(snsSpy.calls))
	}
}

// TestSNSPublishedForReadDesire verifies that ReadDesire upserts also trigger
// SNS notifications with the correct table suffix.
func TestSNSPublishedForReadDesire(t *testing.T) {
	c, _, snsSpy := newTestClientWithSNS()
	ctx := context.Background()

	desire := &ReadDesire{}
	desire.DocumentID = "doc-read-1"
	desire.Spec = ReadDesireSpec{
		ManagementCluster: "staging",
		ClusterID:         "c2",
	}
	specsPrefix := "mc-staging-specs"

	_, err := c.UpsertReadDesire(ctx, specsPrefix, desire)
	if err != nil {
		t.Fatalf("UpsertReadDesire: %v", err)
	}
	if len(snsSpy.calls) != 1 {
		t.Fatalf("expected 1 SNS call, got %d", len(snsSpy.calls))
	}
	if snsSpy.calls[0].tableSuffix != TableSuffixReadDesires {
		t.Errorf("tableSuffix = %q, want %q", snsSpy.calls[0].tableSuffix, TableSuffixReadDesires)
	}
	if snsSpy.calls[0].mcName != "staging" {
		t.Errorf("mcName = %q, want %q", snsSpy.calls[0].mcName, "staging")
	}
}

// TestMCNameFromPrefix verifies the MC name extraction from a specs prefix.
func TestMCNameFromPrefix(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
	}{
		{"mc-prod-specs", "prod"},
		{"mc-staging-specs", "staging"},
		{"mc-mc01-specs", "mc01"},
		{"mc01-specs", "mc01"},
	}
	for _, tc := range cases {
		got := mcNameFromPrefix(tc.prefix)
		if got != tc.want {
			t.Errorf("mcNameFromPrefix(%q) = %q, want %q", tc.prefix, got, tc.want)
		}
	}
}

// TestNoSNSWithoutPublisher verifies that UpsertApplyDesire does not panic
// when no SNSPublisher is configured (NewClient without SNS).
func TestNoSNSWithoutPublisher(t *testing.T) {
	c, _ := newTestClient() // no SNS publisher
	ctx := context.Background()

	desire := &ApplyDesire{}
	desire.DocumentID = "doc-1"
	desire.Spec = ApplyDesireSpec{ClusterID: "c1"}

	// Must not panic.
	if _, err := c.UpsertApplyDesire(ctx, "mc-prod-specs", desire); err != nil {
		t.Fatalf("UpsertApplyDesire without SNS: %v", err)
	}
}
