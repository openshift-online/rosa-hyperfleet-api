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

// Account represents an enabled account in the authorization system
type Account struct {
	AccountID     string `dynamodbav:"accountId" json:"accountId"`
	PolicyStoreID string `dynamodbav:"policyStoreId,omitempty" json:"policyStoreId,omitempty"`
	Privileged    bool   `dynamodbav:"privileged" json:"privileged"`
	CreatedAt     string `dynamodbav:"createdAt" json:"createdAt"`
	CreatedBy     string `dynamodbav:"createdBy" json:"createdBy"`
}

// AccountStore provides CRUD operations for accounts
type AccountStore struct {
	tableName    string
	dynamoClient client.DynamoDBClient
	logger       *slog.Logger
}

// NewAccountStore creates a new account store
func NewAccountStore(tableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger) *AccountStore {
	return &AccountStore{
		tableName:    tableName,
		dynamoClient: dynamoClient,
		logger:       logger,
	}
}

// Create creates a new account entry
func (s *AccountStore) Create(ctx context.Context, account *Account) error {
	if account.CreatedAt == "" {
		account.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	item, err := attributevalue.MarshalMap(account)
	if err != nil {
		return fmt.Errorf("failed to marshal account: %w", err)
	}

	_, err = s.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(accountId)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if ok := isConditionalCheckFailed(err, &condErr); ok {
			return fmt.Errorf("account already exists: %s", account.AccountID)
		}
		return fmt.Errorf("failed to create account: %w", err)
	}

	s.logger.Info("account created", "account_id", account.AccountID, "privileged", account.Privileged)
	return nil
}

// Get retrieves an account by ID
func (s *AccountStore) Get(ctx context.Context, accountID string) (*Account, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	if result.Item == nil {
		return nil, nil
	}

	var account Account
	if err := attributevalue.UnmarshalMap(result.Item, &account); err != nil {
		return nil, fmt.Errorf("failed to unmarshal account: %w", err)
	}

	return &account, nil
}

// Delete removes an account
func (s *AccountStore) Delete(ctx context.Context, accountID string) error {
	_, err := s.dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}

	s.logger.Info("account deleted", "account_id", accountID)
	return nil
}

// List returns all accounts
func (s *AccountStore) List(ctx context.Context) ([]*Account, error) {
	result, err := s.dynamoClient.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(s.tableName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	accounts := make([]*Account, 0, len(result.Items))
	for _, item := range result.Items {
		var account Account
		if err := attributevalue.UnmarshalMap(item, &account); err != nil {
			return nil, fmt.Errorf("failed to unmarshal account: %w", err)
		}
		accounts = append(accounts, &account)
	}

	return accounts, nil
}

// Exists checks if an account exists
func (s *AccountStore) Exists(ctx context.Context, accountID string) (bool, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
		},
		ProjectionExpression: aws.String("accountId"),
	})
	if err != nil {
		return false, fmt.Errorf("failed to check account existence: %w", err)
	}

	return result.Item != nil, nil
}

// UpdatePolicyStoreID updates the policy store ID for an account
func (s *AccountStore) UpdatePolicyStoreID(ctx context.Context, accountID, policyStoreID string) error {
	_, err := s.dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
		},
		UpdateExpression: aws.String("SET policyStoreId = :psid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":psid": &types.AttributeValueMemberS{Value: policyStoreID},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update policy store ID: %w", err)
	}

	s.logger.Info("account policy store ID updated", "account_id", accountID, "policy_store_id", policyStoreID)
	return nil
}

// isConditionalCheckFailed checks if the error is a conditional check failed error
func isConditionalCheckFailed(err error, target **types.ConditionalCheckFailedException) bool {
	if err == nil {
		return false
	}
	// Check if the error message contains the expected text
	if _, ok := err.(*types.ConditionalCheckFailedException); ok {
		return true
	}
	return false
}
