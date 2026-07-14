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
	"github.com/google/uuid"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
)

// Group represents an authorization group
type Group struct {
	AccountID   string `dynamodbav:"accountId" json:"accountId"`
	GroupID     string `dynamodbav:"groupId" json:"groupId"`
	Name        string `dynamodbav:"name" json:"name"`
	Description string `dynamodbav:"description,omitempty" json:"description,omitempty"`
	CreatedAt   string `dynamodbav:"createdAt" json:"createdAt"`
}

// GroupStore provides CRUD operations for groups
type GroupStore struct {
	tableName    string
	dynamoClient client.DynamoDBClient
	logger       *slog.Logger
}

// NewGroupStore creates a new group store
func NewGroupStore(tableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger) *GroupStore {
	return &GroupStore{
		tableName:    tableName,
		dynamoClient: dynamoClient,
		logger:       logger,
	}
}

// Create creates a new group
func (s *GroupStore) Create(ctx context.Context, accountID, name, description string) (*Group, error) {
	group := &Group{
		AccountID:   accountID,
		GroupID:     uuid.New().String(),
		Name:        name,
		Description: description,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	item, err := attributevalue.MarshalMap(group)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal group: %w", err)
	}

	_, err = s.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	s.logger.Info("group created", "account_id", accountID, "group_id", group.GroupID, "name", name)
	return group, nil
}

// Get retrieves a group by ID
func (s *GroupStore) Get(ctx context.Context, accountID, groupID string) (*Group, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
			"groupId":   &types.AttributeValueMemberS{Value: groupID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get group: %w", err)
	}

	if result.Item == nil {
		return nil, nil
	}

	var group Group
	if err := attributevalue.UnmarshalMap(result.Item, &group); err != nil {
		return nil, fmt.Errorf("failed to unmarshal group: %w", err)
	}

	return &group, nil
}

// Delete removes a group
func (s *GroupStore) Delete(ctx context.Context, accountID, groupID string) error {
	_, err := s.dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
			"groupId":   &types.AttributeValueMemberS{Value: groupID},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}

	s.logger.Info("group deleted", "account_id", accountID, "group_id", groupID)
	return nil
}

// List returns all groups for an account
func (s *GroupStore) List(ctx context.Context, accountID string) ([]*Group, error) {
	result, err := s.dynamoClient.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("accountId = :aid"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":aid": &types.AttributeValueMemberS{Value: accountID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}

	groups := make([]*Group, 0, len(result.Items))
	for _, item := range result.Items {
		var group Group
		if err := attributevalue.UnmarshalMap(item, &group); err != nil {
			return nil, fmt.Errorf("failed to unmarshal group: %w", err)
		}
		groups = append(groups, &group)
	}

	return groups, nil
}

// Update updates a group's name and description
func (s *GroupStore) Update(ctx context.Context, accountID, groupID, name, description string) (*Group, error) {
	result, err := s.dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
			"groupId":   &types.AttributeValueMemberS{Value: groupID},
		},
		UpdateExpression: aws.String("SET #n = :name, description = :desc"),
		ExpressionAttributeNames: map[string]string{
			"#n": "name",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":name": &types.AttributeValueMemberS{Value: name},
			":desc": &types.AttributeValueMemberS{Value: description},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update group: %w", err)
	}

	var group Group
	if err := attributevalue.UnmarshalMap(result.Attributes, &group); err != nil {
		return nil, fmt.Errorf("failed to unmarshal group: %w", err)
	}

	s.logger.Info("group updated", "account_id", accountID, "group_id", groupID)
	return &group, nil
}
