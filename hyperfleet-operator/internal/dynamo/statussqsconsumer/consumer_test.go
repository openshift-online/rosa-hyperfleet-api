package statussqsconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// mockSQSClient implements SQSClient for tests.
type mockSQSClient struct {
	messages     []sqstypes.Message
	receiveErr   error
	deleteCalls  []string // receipt handles deleted
	deleteErr    error
	receiveCount int
}

func (m *mockSQSClient) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	m.receiveCount++
	if m.receiveErr != nil {
		return nil, m.receiveErr
	}
	// Return messages on first call, empty on subsequent to avoid infinite loop.
	if m.receiveCount == 1 && len(m.messages) > 0 {
		return &sqs.ReceiveMessageOutput{Messages: m.messages}, nil
	}
	return &sqs.ReceiveMessageOutput{}, nil
}

func (m *mockSQSClient) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	m.deleteCalls = append(m.deleteCalls, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, m.deleteErr
}

func makeMessage(t *testing.T, notification StatusNotification, receipt string) sqstypes.Message {
	t.Helper()
	body, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return sqstypes.Message{
		Body:          aws.String(string(body)),
		ReceiptHandle: aws.String(receipt),
	}
}

func TestConsumer_DispatchesDocumentID(t *testing.T) {
	var dispatched []string

	mock := &mockSQSClient{
		messages: []sqstypes.Message{
			makeMessage(t, StatusNotification{DocumentID: "doc-1", TableSuffix: "-applydesires"}, "rh-1"),
		},
	}

	c := New(mock, "https://sqs.test/queue", func(id string) {
		dispatched = append(dispatched, id)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Run(ctx)

	if len(dispatched) != 1 || dispatched[0] != "doc-1" {
		t.Errorf("dispatched = %v, want [doc-1]", dispatched)
	}
	if len(mock.deleteCalls) != 1 || mock.deleteCalls[0] != "rh-1" {
		t.Errorf("deleteCalls = %v, want [rh-1]", mock.deleteCalls)
	}
}

func TestConsumer_ReadDesireSuffix(t *testing.T) {
	var dispatched []string

	mock := &mockSQSClient{
		messages: []sqstypes.Message{
			makeMessage(t, StatusNotification{DocumentID: "doc-2", TableSuffix: "-readdesires"}, "rh-2"),
		},
	}

	c := New(mock, "https://sqs.test/queue", func(id string) {
		dispatched = append(dispatched, id)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Run(ctx)

	if len(dispatched) != 1 || dispatched[0] != "doc-2" {
		t.Errorf("dispatched = %v, want [doc-2]", dispatched)
	}
}

func TestConsumer_MalformedMessage_Skipped(t *testing.T) {
	var dispatched []string

	mock := &mockSQSClient{
		messages: []sqstypes.Message{
			{Body: aws.String("not-json"), ReceiptHandle: aws.String("rh-bad")},
		},
	}

	c := New(mock, "https://sqs.test/queue", func(id string) {
		dispatched = append(dispatched, id)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Run(ctx)

	if len(dispatched) != 0 {
		t.Errorf("expected no dispatches for malformed message, got %v", dispatched)
	}
	// Malformed messages must still be deleted to avoid queue poisoning.
	if len(mock.deleteCalls) != 1 {
		t.Errorf("expected delete of malformed message, deleteCalls = %v", mock.deleteCalls)
	}
}

func TestConsumer_EmptyDocumentID_Skipped(t *testing.T) {
	var dispatched []string

	mock := &mockSQSClient{
		messages: []sqstypes.Message{
			makeMessage(t, StatusNotification{DocumentID: "", TableSuffix: "-applydesires"}, "rh-empty"),
		},
	}

	c := New(mock, "https://sqs.test/queue", func(id string) {
		dispatched = append(dispatched, id)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Run(ctx)

	if len(dispatched) != 0 {
		t.Errorf("expected no dispatches for empty documentID, got %v", dispatched)
	}
	if len(mock.deleteCalls) != 1 {
		t.Errorf("expected delete of empty-ID message, deleteCalls = %v", mock.deleteCalls)
	}
}

func TestConsumer_DeleteAfterDispatch(t *testing.T) {
	dispatchedBefore := make(chan struct{})
	deleteCallCount := 0

	mock := &mockSQSClient{}
	// Override DeleteMessage to check ordering
	originalDelete := mock.deleteCalls

	msg := makeMessage(t, StatusNotification{DocumentID: "doc-order", TableSuffix: "-applydesires"}, "rh-order")
	mock.messages = []sqstypes.Message{msg}

	var dispatchOrder, deleteOrder int
	callOrder := 0

	customMock := &orderingMock{
		messages: mock.messages,
		onDispatch: func() {
			callOrder++
			dispatchOrder = callOrder
			close(dispatchedBefore)
		},
		onDelete: func() {
			callOrder++
			deleteOrder = callOrder
			deleteCallCount++
		},
	}
	_ = originalDelete

	c := New(customMock, "https://sqs.test/queue", func(id string) {
		customMock.onDispatch()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.Run(ctx)

	if dispatchOrder == 0 || deleteOrder == 0 {
		t.Fatal("dispatch or delete was never called")
	}
	if dispatchOrder >= deleteOrder {
		t.Errorf("dispatch (%d) must happen before delete (%d)", dispatchOrder, deleteOrder)
	}
	_ = dispatchedBefore
}

func TestConsumer_ContextCancel_Stops(t *testing.T) {
	mock := &mockSQSClient{} // no messages — long-poll blocks

	c := New(mock, "https://sqs.test/queue", func(id string) {})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("Run did not stop after context cancel")
	}
}

func TestConsumer_SQSError_Retries(t *testing.T) {
	callCount := 0
	snsErr := errors.New("transient error")

	mock := &mockSQSClient{receiveErr: snsErr}

	c := New(mock, "https://sqs.test/queue", func(id string) {})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.Run(ctx)

	// Should have attempted at least one receive before context cancellation
	callCount = mock.receiveCount
	if callCount == 0 {
		t.Error("expected at least one ReceiveMessage call")
	}
}

// orderingMock lets tests verify dispatch-before-delete ordering.
type orderingMock struct {
	messages   []sqstypes.Message
	callCount  int
	onDispatch func()
	onDelete   func()
}

func (o *orderingMock) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	o.callCount++
	if o.callCount == 1 && len(o.messages) > 0 {
		return &sqs.ReceiveMessageOutput{Messages: o.messages}, nil
	}
	return &sqs.ReceiveMessageOutput{}, nil
}

func (o *orderingMock) DeleteMessage(_ context.Context, _ *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	o.onDelete()
	return &sqs.DeleteMessageOutput{}, nil
}
