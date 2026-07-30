package snspublisher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// mockSNSClient records Publish calls for assertion.
type mockSNSClient struct {
	calls []sns.PublishInput
	err   error
}

func (m *mockSNSClient) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	m.calls = append(m.calls, *in)
	return &sns.PublishOutput{}, m.err
}

func TestTopicARN(t *testing.T) {
	p := New(nil, "us-east-1", "123456789012")
	got := p.TopicARN("prod")
	want := "arn:aws:sns:us-east-1:123456789012:prod-specs-notifications"
	if got != want {
		t.Errorf("TopicARN = %q, want %q", got, want)
	}
}

func TestPublish_CallsSNS(t *testing.T) {
	mock := &mockSNSClient{}
	p := New(mock, "us-east-1", "123456789012")

	err := p.Publish(context.Background(), "prod", "doc-abc", "-applydesires")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 SNS call, got %d", len(mock.calls))
	}

	call := mock.calls[0]
	wantARN := "arn:aws:sns:us-east-1:123456789012:prod-specs-notifications"
	if *call.TopicArn != wantARN {
		t.Errorf("TopicArn = %q, want %q", *call.TopicArn, wantARN)
	}

	var notification SpecNotification
	if err := json.Unmarshal([]byte(*call.Message), &notification); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if notification.DocumentID != "doc-abc" {
		t.Errorf("DocumentID = %q, want %q", notification.DocumentID, "doc-abc")
	}
	if notification.TableSuffix != "-applydesires" {
		t.Errorf("TableSuffix = %q, want %q", notification.TableSuffix, "-applydesires")
	}
}

func TestPublish_ReturnsErrorOnSNSFailure(t *testing.T) {
	simulatedErr := errors.New("simulated SNS error")
	mock := &mockSNSClient{err: simulatedErr}
	p := New(mock, "us-east-1", "123456789012")

	err := p.Publish(context.Background(), "prod", "doc-abc", "-applydesires")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sns publish") {
		t.Errorf("error message %q should mention 'sns publish'", err.Error())
	}
}

func TestPublish_MessageJSON(t *testing.T) {
	mock := &mockSNSClient{}
	p := New(mock, "eu-west-1", "999888777666")

	if err := p.Publish(context.Background(), "staging", "doc-xyz", "-readdesires"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msg := *mock.calls[0].Message
	if !strings.Contains(msg, `"documentID":"doc-xyz"`) {
		t.Errorf("message missing documentID: %s", msg)
	}
	if !strings.Contains(msg, `"tableSuffix":"-readdesires"`) {
		t.Errorf("message missing tableSuffix: %s", msg)
	}
}

func TestPublish_DifferentMCs(t *testing.T) {
	mock := &mockSNSClient{}
	p := New(mock, "us-east-1", "111222333444")

	for _, mc := range []string{"mc01", "mc02", "prod"} {
		if err := p.Publish(context.Background(), mc, "doc-1", "-applydesires"); err != nil {
			t.Fatalf("Publish(%s): %v", mc, err)
		}
	}
	if len(mock.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(mock.calls))
	}
	wantARNs := []string{
		"arn:aws:sns:us-east-1:111222333444:mc01-specs-notifications",
		"arn:aws:sns:us-east-1:111222333444:mc02-specs-notifications",
		"arn:aws:sns:us-east-1:111222333444:prod-specs-notifications",
	}
	for i, want := range wantARNs {
		if *mock.calls[i].TopicArn != want {
			t.Errorf("call[%d] TopicArn = %q, want %q", i, *mock.calls[i].TopicArn, want)
		}
	}
}
