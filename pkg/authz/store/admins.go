package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
)

// Admin represents an admin entry for an account
type Admin struct {
	AccountID    string `dynamodbav:"accountId" json:"accountId"`
	PrincipalARN string `dynamodbav:"principalArn" json:"principalArn"`
	CreatedAt    string `dynamodbav:"createdAt" json:"createdAt"`
	CreatedBy    string `dynamodbav:"createdBy" json:"createdBy"`
}

// AdminStore provides CRUD operations for admins
type AdminStore struct {
	tableName    string
	dynamoClient client.DynamoDBClient
	logger       *slog.Logger
}

// NewAdminStore creates a new admin store
func NewAdminStore(tableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger) *AdminStore {
	return &AdminStore{
		tableName:    tableName,
		dynamoClient: dynamoClient,
		logger:       logger,
	}
}

// Add adds an admin for an account
func (s *AdminStore) Add(ctx context.Context, admin *Admin) error {
	if admin.CreatedAt == "" {
		admin.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	item, err := attributevalue.MarshalMap(admin)
	if err != nil {
		return fmt.Errorf("failed to marshal admin: %w", err)
	}

	_, err = s.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(accountId) AND attribute_not_exists(principalArn)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if ok := isConditionalCheckFailed(err, &condErr); ok {
			return fmt.Errorf("admin already exists: %s in account %s", admin.PrincipalARN, admin.AccountID)
		}
		return fmt.Errorf("failed to add admin: %w", err)
	}

	s.logger.Info("admin added", "account_id", admin.AccountID, "principal_arn", admin.PrincipalARN)
	return nil
}

// Remove removes an admin from an account
func (s *AdminStore) Remove(ctx context.Context, accountID, principalARN string) error {
	_, err := s.dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId":    &types.AttributeValueMemberS{Value: accountID},
			"principalArn": &types.AttributeValueMemberS{Value: principalARN},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to remove admin: %w", err)
	}

	s.logger.Info("admin removed", "account_id", accountID, "principal_arn", principalARN)
	return nil
}

// IsAdmin checks if a principal is an admin for an account
func (s *AdminStore) IsAdmin(ctx context.Context, accountID, principalARN string) (bool, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId":    &types.AttributeValueMemberS{Value: accountID},
			"principalArn": &types.AttributeValueMemberS{Value: principalARN},
		},
		ProjectionExpression: aws.String("accountId"),
	})
	if err != nil {
		return false, fmt.Errorf("failed to check admin status: %w", err)
	}

	return result.Item != nil, nil
}

// List returns all admins for an account
func (s *AdminStore) List(ctx context.Context, accountID string) ([]*Admin, error) {
	result, err := s.dynamoClient.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("accountId = :aid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":aid": &types.AttributeValueMemberS{Value: accountID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list admins: %w", err)
	}

	admins := make([]*Admin, 0, len(result.Items))
	for _, item := range result.Items {
		var admin Admin
		if err := attributevalue.UnmarshalMap(item, &admin); err != nil {
			return nil, fmt.Errorf("failed to unmarshal admin: %w", err)
		}
		admins = append(admins, &admin)
	}

	return admins, nil
}

// ListARNs returns the ARNs of all admins for an account
func (s *AdminStore) ListARNs(ctx context.Context, accountID string) ([]string, error) {
	admins, err := s.List(ctx, accountID)
	if err != nil {
		return nil, err
	}

	arns := make([]string, len(admins))
	for i, admin := range admins {
		arns[i] = admin.PrincipalARN
	}

	return arns, nil
}
