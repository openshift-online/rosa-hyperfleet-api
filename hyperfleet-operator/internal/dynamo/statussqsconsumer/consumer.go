// Package statussqsconsumer polls an SQS queue for status change notifications
// published by kube-applier-aws after writing a status document to DynamoDB.
// On each notification the consumer invokes onDocumentID so the operator's
// EventRouter can dispatch the document ID to the appropriate controller
// workqueue for immediate re-reconciliation.
//
// This replaces the DynamoDB Streams-based statusstream.Manager as the
// incremental status change notification mechanism. The operator pre-provisions
// one SQS queue per replica (named after its pod hostname) and polls only its
// own queue, eliminating competing-consumer issues.
package statussqsconsumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	// maxMessages is the maximum number of SQS messages to retrieve per poll.
	maxMessages = 10

	// waitTimeSeconds is the SQS long-poll duration. The call blocks up to
	// this many seconds when the queue is empty, avoiding a busy loop.
	waitTimeSeconds = 20

	// retryDelay is the pause between retries after an SQS error.
	retryDelay = 5 * time.Second
)

// SQSClient is the subset of the AWS SQS API used by Consumer.
// It is a narrow interface so that tests can substitute a mock.
type SQSClient interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// StatusNotification is the JSON payload delivered by EventBridge Pipes (via SQS)
// after kube-applier writes a status document to DynamoDB. The pipe's
// input_template extracts the partition key and table suffix from the DynamoDB
// stream record.
type StatusNotification struct {
	DocumentID  string `json:"documentID"`
	TableSuffix string `json:"tableSuffix"` // e.g. "-applydesires" or "-readdesires"
}

// Consumer receives StatusNotification messages from an SQS queue and invokes
// onDocumentID for each valid document ID so the EventRouter can dispatch it.
type Consumer struct {
	client       SQSClient
	queueURL     string
	onDocumentID func(documentID string)
	logger       *slog.Logger
}

// New returns a Consumer. onDocumentID is called for every successfully decoded
// document ID; the caller's EventRouter.Dispatch silently drops IDs it does
// not own, so no per-MC filtering is needed here.
func New(client SQSClient, queueURL string, onDocumentID func(documentID string)) *Consumer {
	return &Consumer{
		client:       client,
		queueURL:     queueURL,
		onDocumentID: onDocumentID,
		logger:       slog.Default().With("component", "statussqsconsumer", "queueURL", queueURL),
	}
}

// Run polls the SQS queue continuously until ctx is cancelled.
// It should be started in a goroutine.
func (c *Consumer) Run(ctx context.Context) {
	c.logger.Info("status SQS consumer started")
	defer c.logger.Info("status SQS consumer stopped")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgs, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: maxMessages,
			WaitTimeSeconds:     waitTimeSeconds,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Error("failed to receive SQS messages; retrying",
				"err", err, "retryDelay", retryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}

		for _, msg := range msgs.Messages {
			c.handleMessage(ctx, msg.Body, msg.ReceiptHandle)
		}
	}
}

func (c *Consumer) handleMessage(ctx context.Context, body *string, receiptHandle *string) {
	if body == nil {
		return
	}

	var notification StatusNotification
	if err := json.Unmarshal([]byte(*body), &notification); err != nil {
		c.logger.Error("failed to unmarshal SQS message; deleting",
			"err", err)
		c.deleteMessage(ctx, receiptHandle)
		return
	}

	if notification.DocumentID == "" {
		c.logger.Info("received SQS message with empty documentID; skipping")
		c.deleteMessage(ctx, receiptHandle)
		return
	}

	// Guard against misconfigured EventBridge Pipes input_template: if the pipe
	// used jsonencode() instead of a raw string, the JSONPath placeholder is
	// delivered literally. Detect this so the misconfiguration is obvious rather
	// than silently dropped by the EventRouter.
	if strings.HasPrefix(notification.DocumentID, "<") && strings.HasSuffix(notification.DocumentID, ">") {
		c.logger.Error("documentID looks like an unresolved EventBridge JSONPath placeholder; "+
			"check that the pipe's input_template uses a raw string (not jsonencode) and that "+
			"the JSONPath <$.dynamodb.Keys.documentID.S> resolves against the stream record",
			"documentID", notification.DocumentID,
			"tableSuffix", notification.TableSuffix,
		)
		c.deleteMessage(ctx, receiptHandle)
		return
	}

	c.logger.Debug("dispatching status notification",
		"documentID", notification.DocumentID,
		"tableSuffix", notification.TableSuffix,
	)
	c.onDocumentID(notification.DocumentID)

	// Delete after dispatch. If the process crashes before this point the
	// message becomes visible again after the visibility timeout and will be
	// re-delivered — dispatch is idempotent.
	c.deleteMessage(ctx, receiptHandle)
}

func (c *Consumer) deleteMessage(ctx context.Context, receiptHandle *string) {
	if receiptHandle == nil {
		return
	}
	if _, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: receiptHandle,
	}); err != nil {
		if ctx.Err() == nil {
			c.logger.Error("failed to delete SQS message",
				"err", err, "receiptHandle", *receiptHandle)
		}
	}
}
