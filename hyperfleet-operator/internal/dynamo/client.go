package dynamo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"k8s.io/apimachinery/pkg/runtime"
)

// ErrNotFound is returned when a desire item does not exist in DynamoDB.
var ErrNotFound = errors.New("desire not found")

const (
	TableSuffixApplyDesires      = "-applydesires"
	TableSuffixReadDesires       = "-readdesires"
	TableSuffixStatusApplyDesires = "-status-applydesires"
	TableSuffixStatusReadDesires  = "-status-readdesires"
	attributeDocumentID           = "documentID"
)

type dynamoAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// SNSPublisher publishes a spec change notification after a desire is written.
// Implementations must be safe for concurrent use. A nil SNSPublisher disables
// notifications (useful for tests and local development).
type SNSPublisher interface {
	// Publish sends a notification for the given document to the SNS topic for
	// mcName. tableSuffix identifies the table type (e.g. "-applydesires").
	// Errors are best-effort: callers should log them but need not propagate.
	Publish(ctx context.Context, mcName, documentID, tableSuffix string) error
}

// UpsertResult reports whether an upsert changed the item and the updateTime
// that should be used for staleness tracking. When Changed is false, UpdateTime
// reflects the existing item's time so callers never need to fabricate one.
type UpsertResult struct {
	Changed    bool
	UpdateTime time.Time
}

// DesireClient is the interface used by controllers to interact with DynamoDB desires.
type DesireClient interface {
	UpsertApplyDesire(ctx context.Context, specsPrefix string, desire *ApplyDesire) (UpsertResult, error)
	UpsertReadDesire(ctx context.Context, specsPrefix string, desire *ReadDesire) (UpsertResult, error)
	GetApplyDesireStatus(ctx context.Context, statusPrefix, documentID string) (*ApplyDesireStatus, error)
	GetReadDesireStatus(ctx context.Context, statusPrefix, documentID string) (*ReadDesireStatus, error)
	DeleteDesireSpec(ctx context.Context, specsPrefix, suffix, documentID string) error
}

type cacheEntry struct {
	specHash   string
	updateTime time.Time
}

// Client writes desire specs and reads desire statuses from DynamoDB.
// It is the inverse of kube-applier-aws: the operator writes specs and reads
// statuses, while kube-applier reads specs and writes statuses.
type Client struct {
	db    dynamoAPI
	cache sync.Map // table/documentID → cacheEntry
	sns   SNSPublisher
}

var _ DesireClient = (*Client)(nil)

// NewClient returns a Client with no SNS publisher. Desire writes succeed but
// no SNS notifications are sent. Use NewClientWithSNS for production.
func NewClient(db dynamoAPI) *Client {
	return &Client{db: db}
}

// NewClientWithSNS returns a Client that publishes a spec change notification
// to SNS after every desire write where the spec actually changed.
func NewClientWithSNS(db dynamoAPI, publisher SNSPublisher) *Client {
	return &Client{db: db, sns: publisher}
}

// UpsertApplyDesire writes an ApplyDesire spec only when content has changed.
// If the spec changed and an SNSPublisher is configured, it publishes a
// notification so kube-applier learns about the change without polling Streams.
func (c *Client) UpsertApplyDesire(ctx context.Context, specsPrefix string, desire *ApplyDesire) (UpsertResult, error) {
	result, err := c.upsertDesire(ctx, specsPrefix+TableSuffixApplyDesires, desire.DocumentID, desire.Spec)
	if err != nil {
		return result, err
	}
	if result.Changed {
		c.publishNotification(ctx, specsPrefix, desire.DocumentID, TableSuffixApplyDesires)
	}
	return result, nil
}

// UpsertReadDesire writes a ReadDesire spec only when content has changed.
// If the spec changed and an SNSPublisher is configured, it publishes a
// notification so kube-applier learns about the change without polling Streams.
func (c *Client) UpsertReadDesire(ctx context.Context, specsPrefix string, desire *ReadDesire) (UpsertResult, error) {
	result, err := c.upsertDesire(ctx, specsPrefix+TableSuffixReadDesires, desire.DocumentID, desire.Spec)
	if err != nil {
		return result, err
	}
	if result.Changed {
		c.publishNotification(ctx, specsPrefix, desire.DocumentID, TableSuffixReadDesires)
	}
	return result, nil
}

// publishNotification sends an SNS notification for a changed desire write.
// It is a no-op when no SNSPublisher is configured. Errors are logged but not
// propagated — kube-applier's 5-minute safety-net poll covers missed events.
func (c *Client) publishNotification(ctx context.Context, specsPrefix, documentID, tableSuffix string) {
	if c.sns == nil {
		return
	}
	mcName := mcNameFromPrefix(specsPrefix)
	if err := c.sns.Publish(ctx, mcName, documentID, tableSuffix); err != nil {
		slog.Error("Failed to publish desire change notification to SNS",
			"mcName", mcName,
			"documentID", documentID,
			"tableSuffix", tableSuffix,
			"error", err,
		)
	}
}

// mcNameFromPrefix strips the "-specs" suffix from a specsPrefix to recover the
// management cluster name. E.g. "eph-45df5708-mc01-specs" → "eph-45df5708-mc01",
// "mc01-specs" → "mc01". SpecsPrefix is the inverse: SpecsPrefix(mc) = mc+"-specs".
func mcNameFromPrefix(specsPrefix string) string {
	return strings.TrimSuffix(specsPrefix, "-specs")
}

// GetApplyDesireStatus reads an ApplyDesire from the status table.
func (c *Client) GetApplyDesireStatus(ctx context.Context, statusPrefix, documentID string) (*ApplyDesireStatus, error) {
	var status ApplyDesireStatus
	if err := c.getDesireStatus(ctx, statusPrefix+TableSuffixApplyDesires, documentID, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetReadDesireStatus reads a ReadDesire from the status table.
func (c *Client) GetReadDesireStatus(ctx context.Context, statusPrefix, documentID string) (*ReadDesireStatus, error) {
	var status ReadDesireStatus
	if err := c.getDesireStatus(ctx, statusPrefix+TableSuffixReadDesires, documentID, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// DeleteDesireSpec removes a desire from the specs table.
func (c *Client) DeleteDesireSpec(ctx context.Context, specsPrefix, suffix, documentID string) error {
	table := specsPrefix + suffix
	_, err := c.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(table),
		Key: map[string]dynamodbtypes.AttributeValue{
			attributeDocumentID: &dynamodbtypes.AttributeValueMemberS{Value: documentID},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamodb delete %s/%s: %w", table, documentID, err)
	}
	c.cache.Delete(c.cacheKey(table, documentID))
	return nil
}

func computeSpecHash(spec any) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal spec for hash: %w", err)
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}

func (c *Client) cacheKey(table, documentID string) string {
	return table + "/" + documentID
}

func (c *Client) upsertDesire(ctx context.Context, table, documentID string, spec any) (UpsertResult, error) {
	newHash, err := computeSpecHash(spec)
	if err != nil {
		return UpsertResult{}, err
	}

	// Fast path: if we wrote this item before and the spec hasn't changed, skip DynamoDB entirely.
	ck := c.cacheKey(table, documentID)
	if v, ok := c.cache.Load(ck); ok {
		entry := v.(cacheEntry)
		if entry.specHash == newHash {
			return UpsertResult{Changed: false, UpdateTime: entry.updateTime}, nil
		}
	}

	// Cache miss — fall through to DynamoDB read (cold start or spec changed).
	existing, err := c.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:            aws.String(table),
		ProjectionExpression: aws.String("specHash, updateTime"),
		Key: map[string]dynamodbtypes.AttributeValue{
			attributeDocumentID: &dynamodbtypes.AttributeValueMemberS{Value: documentID},
		},
	})
	if err != nil {
		return UpsertResult{}, fmt.Errorf("dynamodb get %s/%s for upsert: %w", table, documentID, err)
	}

	if len(existing.Item) > 0 {
		if hashAttr, ok := existing.Item["specHash"]; ok {
			if s, ok := hashAttr.(*dynamodbtypes.AttributeValueMemberS); ok && s.Value == newHash {
				var existingTime time.Time
				if utAttr, ok := existing.Item["updateTime"]; ok {
					if ts, ok := utAttr.(*dynamodbtypes.AttributeValueMemberS); ok {
						var parseErr error
						existingTime, parseErr = time.Parse(time.RFC3339, ts.Value)
						if parseErr != nil {
							slog.Warn("failed to parse updateTime from DynamoDB",
								"table", table, "documentID", documentID, "raw", ts.Value, "error", parseErr)
						}
					}
				}
				c.cache.Store(ck, cacheEntry{specHash: newHash, updateTime: existingTime})
				return UpsertResult{Changed: false, UpdateTime: existingTime}, nil
			}
		}
	}

	// Truncate to second precision to match RFC3339 storage in DynamoDB.
	// Without this, the cached nanosecond-precision time would always appear
	// "after" the second-precision ObservedDesireUpdateTime that kube-applier
	// copies from the stored value, causing staleness checks to fail.
	now := time.Now().UTC().Truncate(time.Second)
	if err := c.putDesireWithHash(ctx, table, documentID, spec, newHash, now); err != nil {
		return UpsertResult{}, err
	}
	c.cache.Store(ck, cacheEntry{specHash: newHash, updateTime: now})
	return UpsertResult{Changed: true, UpdateTime: now}, nil
}

func (c *Client) putDesireWithHash(ctx context.Context, table, documentID string, spec any, specHash string, updateTime time.Time) error {
	specAttrs, err := attributevalue.MarshalMap(spec)
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}

	item := map[string]dynamodbtypes.AttributeValue{
		attributeDocumentID: &dynamodbtypes.AttributeValueMemberS{Value: documentID},
		"version":           &dynamodbtypes.AttributeValueMemberN{Value: "1"},
		"updateTime":        &dynamodbtypes.AttributeValueMemberS{Value: updateTime.Format(time.RFC3339)},
		"spec":              &dynamodbtypes.AttributeValueMemberM{Value: specAttrs},
	}

	if specHash != "" {
		item["specHash"] = &dynamodbtypes.AttributeValueMemberS{Value: specHash}
	}

	if specMap, ok := spec.(ApplyDesireSpec); ok && specMap.ServerSideApply != nil && specMap.ServerSideApply.KubeContent != nil {
		item["spec_kubeContent"] = &dynamodbtypes.AttributeValueMemberS{Value: string(specMap.ServerSideApply.KubeContent.Raw)}
		if ssa, ok := specAttrs["serverSideApply"]; ok {
			if ssaMap, ok := ssa.(*dynamodbtypes.AttributeValueMemberM); ok {
				delete(ssaMap.Value, "kubeContent")
			}
		}
	}

	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("dynamodb put %s/%s: %w", table, documentID, err)
	}
	return nil
}

func (c *Client) getDesireStatus(ctx context.Context, table, documentID string, out any) error {
	result, err := c.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(table),
		ConsistentRead: aws.Bool(true),
		Key: map[string]dynamodbtypes.AttributeValue{
			attributeDocumentID: &dynamodbtypes.AttributeValueMemberS{Value: documentID},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamodb get %s/%s: %w", table, documentID, err)
	}
	if len(result.Item) == 0 {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, table, documentID)
	}

	// kube-applier-aws writes the status fields nested under a "status" key.
	// Extract that map for unmarshalling into the status struct.
	statusAttrs := result.Item
	if statusM, ok := result.Item["status"]; ok {
		if m, ok := statusM.(*dynamodbtypes.AttributeValueMemberM); ok {
			statusAttrs = m.Value
		}
	}

	if err := attributevalue.UnmarshalMap(statusAttrs, out); err != nil {
		return fmt.Errorf("unmarshal %s/%s: %w", table, documentID, err)
	}

	// ReadDesireStatus.KubeContent is tagged dynamodbav:"-" in the shared type
	// (mirroring kube-applier-aws's codec), so the standard unmarshaller skips
	// it. Manually populate it from the top-level status_kubeContent attribute
	// that kube-applier-aws writes.
	if rs, ok := out.(*ReadDesireStatus); ok {
		if av, ok := result.Item["status_kubeContent"]; ok {
			if sv, ok := av.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
				rs.KubeContent = &runtime.RawExtension{Raw: []byte(sv.Value)}
			}
		}
	}

	return nil
}

// ResetCache clears the in-memory write-cache. This is intended for test
// teardown where DynamoDB tables are purged between specs.
func (c *Client) ResetCache() {
	c.cache.Clear()
}

// TablePrefix returns the specs or status table prefix for a management cluster.
func SpecsPrefix(mc string) string {
	return fmt.Sprintf("%s-specs", mc)
}

func StatusPrefix(mc string) string {
	return fmt.Sprintf("%s-status", mc)
}
