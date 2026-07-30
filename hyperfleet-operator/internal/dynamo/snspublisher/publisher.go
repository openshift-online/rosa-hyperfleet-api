// Package snspublisher publishes spec change notifications to SNS after a
// desire document is written to DynamoDB. kube-applier polls its own SQS
// queue (subscribed to the SNS topic) to learn about changes without tailing
// DynamoDB Streams.
package snspublisher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// SNSClient is the subset of the AWS SNS API used by Publisher.
// It is a narrow interface so that tests can substitute a mock.
type SNSClient interface {
	Publish(ctx context.Context, in *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// SpecNotification is the JSON payload sent to SNS after a desire write.
// kube-applier decodes this payload from its SQS queue to know which document
// changed and in which table.
type SpecNotification struct {
	DocumentID  string `json:"documentID"`
	TableSuffix string `json:"tableSuffix"` // e.g. "-applydesires" or "-readdesires"
}

// Publisher constructs SNS topic ARNs deterministically from the MC name and
// publishes SpecNotification messages after each changed desire write.
type Publisher struct {
	client    SNSClient
	region    string
	accountID string
}

// New returns a Publisher that constructs topic ARNs as:
//
//	arn:aws:sns:<region>:<accountID>:<mcName>-specs-notifications
func New(client SNSClient, region, accountID string) *Publisher {
	return &Publisher{
		client:    client,
		region:    region,
		accountID: accountID,
	}
}

// TopicARN returns the deterministic SNS topic ARN for the given MC name.
// The topic name matches the Terraform convention: ${mc_name}-specs-notifications.
func (p *Publisher) TopicARN(mcName string) string {
	return fmt.Sprintf("arn:aws:sns:%s:%s:%s-specs-notifications", p.region, p.accountID, mcName)
}

// Publish sends a SpecNotification to the SNS topic for mcName. It is
// best-effort: on failure it returns an error that callers should log but
// need not propagate — kube-applier's 5-minute safety-net poll covers missed
// notifications.
func (p *Publisher) Publish(ctx context.Context, mcName, documentID, tableSuffix string) error {
	notification := SpecNotification{
		DocumentID:  documentID,
		TableSuffix: tableSuffix,
	}
	body, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal SNS notification: %w", err)
	}

	topicARN := p.TopicARN(mcName)
	_, err = p.client.Publish(ctx, &sns.PublishInput{
		TopicArn: aws.String(topicARN),
		Message:  aws.String(string(body)),
	})
	if err != nil {
		return fmt.Errorf("sns publish to %s: %w", topicARN, err)
	}
	return nil
}
