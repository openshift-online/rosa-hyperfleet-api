package zoa

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
)

// ListFilter defines optional filters for listing executions.
type ListFilter struct {
	Status        string
	Action        string
	TargetCluster string
	Operator      string
	Scope         string
	Type          string
	OutputStatus  string
	ApprovalState string
	DryRun        *bool
	Force         *bool
	Since         string // RFC3339 timestamp
}

// ExecutionStore provides CRUD operations for ZOA executions.
type ExecutionStore interface {
	Create(ctx context.Context, exec *Execution) error
	Get(ctx context.Context, executionID string) (*Execution, error)
	List(ctx context.Context, accountID string, limit int, filter *ListFilter) ([]*Execution, error)
	UpdateStatus(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error
	UpdateCompletion(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error
	UpdateManifestWorkName(ctx context.Context, executionID, mwName string) error
	ListPending(ctx context.Context) ([]*Execution, error)
}

// DynamoExecutionStore implements ExecutionStore backed by DynamoDB.
type DynamoExecutionStore struct {
	tableName    string
	dynamoClient client.DynamoDBClient
	logger       *slog.Logger
	ttlDays      int
}

// NewDynamoExecutionStore creates a new DynamoDB-backed execution store.
func NewDynamoExecutionStore(tableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger, ttlDays int) *DynamoExecutionStore {
	if ttlDays <= 0 {
		ttlDays = 365
	}
	return &DynamoExecutionStore{
		tableName:    tableName,
		dynamoClient: dynamoClient,
		logger:       logger,
		ttlDays:      ttlDays,
	}
}

func (s *DynamoExecutionStore) Create(ctx context.Context, exec *Execution) error {
	now := time.Now().UTC()
	if exec.CreatedAt == "" {
		exec.CreatedAt = now.Format(time.RFC3339)
	}
	exec.UpdatedAt = now.Format(time.RFC3339)
	exec.TTL = now.AddDate(0, 0, s.ttlDays).Unix()

	item, err := attributevalue.MarshalMap(exec)
	if err != nil {
		return fmt.Errorf("failed to marshal execution: %w", err)
	}

	_, err = s.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(executionId)"),
	})
	if err != nil {
		return fmt.Errorf("failed to create execution: %w", err)
	}

	s.logger.Info("execution created", "execution_id", exec.ExecutionID, "action", exec.Action)
	return nil
}

func (s *DynamoExecutionStore) Get(ctx context.Context, executionID string) (*Execution, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"executionId": &types.AttributeValueMemberS{Value: executionID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get execution: %w", err)
	}

	if result.Item == nil {
		return nil, nil
	}

	var exec Execution
	if err := attributevalue.UnmarshalMap(result.Item, &exec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal execution: %w", err)
	}

	return &exec, nil
}

func (s *DynamoExecutionStore) List(ctx context.Context, accountID string, limit int, filter *ListFilter) ([]*Execution, error) {
	exprNames := map[string]string{}
	exprValues := map[string]types.AttributeValue{
		":aid": &types.AttributeValueMemberS{Value: accountID},
	}

	keyCondition := "accountId = :aid"
	var filterParts []string

	if filter != nil {
		if filter.Status != "" {
			filterParts = append(filterParts, "#fld_status = :fstatus")
			exprNames["#fld_status"] = "status"
			exprValues[":fstatus"] = &types.AttributeValueMemberS{Value: filter.Status}
		}
		if filter.Action != "" {
			filterParts = append(filterParts, "#fld_action = :faction")
			exprNames["#fld_action"] = "action"
			exprValues[":faction"] = &types.AttributeValueMemberS{Value: filter.Action}
		}
		if filter.TargetCluster != "" {
			filterParts = append(filterParts, "#fld_target = :ftarget")
			exprNames["#fld_target"] = "targetCluster"
			exprValues[":ftarget"] = &types.AttributeValueMemberS{Value: filter.TargetCluster}
		}
		if filter.Operator != "" {
			filterParts = append(filterParts, "#fld_operator = :foperator")
			exprNames["#fld_operator"] = "operator"
			exprValues[":foperator"] = &types.AttributeValueMemberS{Value: filter.Operator}
		}
		if filter.Scope != "" {
			filterParts = append(filterParts, "#fld_scope = :fscope")
			exprNames["#fld_scope"] = "scope"
			exprValues[":fscope"] = &types.AttributeValueMemberS{Value: filter.Scope}
		}
		if filter.Type != "" {
			filterParts = append(filterParts, "#fld_type = :ftype")
			exprNames["#fld_type"] = "type"
			exprValues[":ftype"] = &types.AttributeValueMemberS{Value: filter.Type}
		}
		if filter.OutputStatus != "" {
			filterParts = append(filterParts, "#fld_output_status = :foutput_status")
			exprNames["#fld_output_status"] = "outputStatus"
			exprValues[":foutput_status"] = &types.AttributeValueMemberS{Value: filter.OutputStatus}
		}
		if filter.ApprovalState != "" {
			filterParts = append(filterParts, "#fld_approval = :fapproval")
			exprNames["#fld_approval"] = "approvalState"
			exprValues[":fapproval"] = &types.AttributeValueMemberS{Value: filter.ApprovalState}
		}
		if filter.DryRun != nil {
			filterParts = append(filterParts, "#fld_dryrun = :fdryrun")
			exprNames["#fld_dryrun"] = "dryRun"
			exprValues[":fdryrun"] = &types.AttributeValueMemberBOOL{Value: *filter.DryRun}
		}
		if filter.Force != nil {
			filterParts = append(filterParts, "#fld_force = :fforce")
			exprNames["#fld_force"] = "force"
			exprValues[":fforce"] = &types.AttributeValueMemberBOOL{Value: *filter.Force}
		}
		if filter.Since != "" {
			keyCondition += " AND #fld_created >= :fsince"
			exprNames["#fld_created"] = "createdAt"
			exprValues[":fsince"] = &types.AttributeValueMemberS{Value: filter.Since}
		}
	}

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(s.tableName),
		IndexName:                 aws.String("account-index"),
		KeyConditionExpression:    aws.String(keyCondition),
		ExpressionAttributeValues: exprValues,
		ScanIndexForward:          aws.Bool(false),
	}

	if len(filterParts) > 0 {
		filterExpr := strings.Join(filterParts, " AND ")
		input.FilterExpression = aws.String(filterExpr)
	}

	if len(exprNames) > 0 {
		input.ExpressionAttributeNames = exprNames
	}

	if limit > 0 {
		input.Limit = aws.Int32(int32(limit))
	}

	result, err := s.dynamoClient.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list executions: %w", err)
	}

	executions := make([]*Execution, 0, len(result.Items))
	for _, item := range result.Items {
		var exec Execution
		if err := attributevalue.UnmarshalMap(item, &exec); err != nil {
			s.logger.Error("failed to unmarshal execution item", "error", err)
			continue
		}
		executions = append(executions, &exec)
	}

	return executions, nil
}

func (s *DynamoExecutionStore) UpdateStatus(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	updateExpr := "SET #status = :s, updatedAt = :ua"
	exprNames := map[string]string{"#status": "status"}
	exprValues := map[string]types.AttributeValue{
		":s":  &types.AttributeValueMemberS{Value: string(status)},
		":ua": &types.AttributeValueMemberS{Value: now},
	}

	if completedAt != "" {
		updateExpr += ", completedAt = :c, #dur = :d"
		exprNames["#dur"] = "duration"
		exprValues[":c"] = &types.AttributeValueMemberS{Value: completedAt}
		exprValues[":d"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", duration)}
	}

	_, err := s.dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"executionId": &types.AttributeValueMemberS{Value: executionID},
		},
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprValues,
	})
	if err != nil {
		return fmt.Errorf("failed to update execution status: %w", err)
	}

	s.logger.Info("execution status updated", "execution_id", executionID, "status", status)
	return nil
}

func (s *DynamoExecutionStore) UpdateCompletion(ctx context.Context, executionID string, status ExecutionStatus, completedAt string, duration int, runnerSeconds int, uploadSeconds int, outputStatus OutputStatus) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"executionId": &types.AttributeValueMemberS{Value: executionID},
		},
		UpdateExpression: aws.String("SET #status = :s, completedAt = :c, durationSeconds = :d, runnerSeconds = :r, uploadSeconds = :u, outputStatus = :os, updatedAt = :ua"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":  &types.AttributeValueMemberS{Value: string(status)},
			":c":  &types.AttributeValueMemberS{Value: completedAt},
			":d":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", duration)},
			":r":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", runnerSeconds)},
			":u":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", uploadSeconds)},
			":os": &types.AttributeValueMemberS{Value: string(outputStatus)},
			":ua": &types.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update execution completion: %w", err)
	}

	s.logger.Info("execution completion updated",
		"execution_id", executionID,
		"status", status,
		"output_status", outputStatus,
		"runner_seconds", runnerSeconds,
		"upload_seconds", uploadSeconds,
		"duration_seconds", duration,
	)
	return nil
}

func (s *DynamoExecutionStore) UpdateManifestWorkName(ctx context.Context, executionID, mwName string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"executionId": &types.AttributeValueMemberS{Value: executionID},
		},
		UpdateExpression: aws.String("SET manifestWorkName = :mw, updatedAt = :ua"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":mw": &types.AttributeValueMemberS{Value: mwName},
			":ua": &types.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update manifestwork name: %w", err)
	}
	return nil
}

func (s *DynamoExecutionStore) ListPending(ctx context.Context) ([]*Execution, error) {
	executions := make([]*Execution, 0)

	for _, status := range []ExecutionStatus{StatusPending, StatusRunning} {
		result, err := s.dynamoClient.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.tableName),
			IndexName:              aws.String("status-index"),
			KeyConditionExpression: aws.String("#status = :status"),
			ExpressionAttributeNames: map[string]string{
				"#status": "status",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":status": &types.AttributeValueMemberS{Value: string(status)},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query %s executions: %w", status, err)
		}

		for _, item := range result.Items {
			var exec Execution
			if err := attributevalue.UnmarshalMap(item, &exec); err != nil {
				s.logger.Error("failed to unmarshal execution item", "error", err)
				continue
			}
			executions = append(executions, &exec)
		}
	}

	return executions, nil
}
