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

// GroupMember represents a group membership
type GroupMember struct {
	AccountID string `dynamodbav:"accountId" json:"accountId"`
	// Composite sort key: groupId#memberArn
	GroupIDMemberARN string `dynamodbav:"groupId#memberArn" json:"-"`
	GroupID          string `dynamodbav:"groupId" json:"groupId"`
	MemberARN        string `dynamodbav:"memberArn" json:"memberArn"`
	AddedAt          string `dynamodbav:"addedAt" json:"addedAt"`
	// GSI attribute: accountId#memberArn for member-groups-index
	AccountIDMemberARN string `dynamodbav:"accountId#memberArn" json:"-"`
}

// MemberStore provides CRUD operations for group members
type MemberStore struct {
	tableName    string
	dynamoClient client.DynamoDBClient
	logger       *slog.Logger
}

// NewMemberStore creates a new member store
func NewMemberStore(tableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger) *MemberStore {
	return &MemberStore{
		tableName:    tableName,
		dynamoClient: dynamoClient,
		logger:       logger,
	}
}

// Add adds a member to a group
func (s *MemberStore) Add(ctx context.Context, accountID, groupID, memberARN string) error {
	member := &GroupMember{
		AccountID:          accountID,
		GroupIDMemberARN:   fmt.Sprintf("%s#%s", groupID, memberARN),
		GroupID:            groupID,
		MemberARN:          memberARN,
		AddedAt:            time.Now().UTC().Format(time.RFC3339),
		AccountIDMemberARN: fmt.Sprintf("%s#%s", accountID, memberARN),
	}

	item, err := attributevalue.MarshalMap(member)
	if err != nil {
		return fmt.Errorf("failed to marshal member: %w", err)
	}

	_, err = s.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("failed to add member: %w", err)
	}

	s.logger.Info("member added to group", "account_id", accountID, "group_id", groupID, "member_arn", memberARN)
	return nil
}

// Remove removes a member from a group
func (s *MemberStore) Remove(ctx context.Context, accountID, groupID, memberARN string) error {
	_, err := s.dynamoClient.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId":         &types.AttributeValueMemberS{Value: accountID},
			"groupId#memberArn": &types.AttributeValueMemberS{Value: fmt.Sprintf("%s#%s", groupID, memberARN)},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}

	s.logger.Info("member removed from group", "account_id", accountID, "group_id", groupID, "member_arn", memberARN)
	return nil
}

// ListGroupMembers returns all members of a group
func (s *MemberStore) ListGroupMembers(ctx context.Context, accountID, groupID string) ([]string, error) {
	result, err := s.dynamoClient.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		KeyConditionExpression: aws.String("accountId = :aid AND begins_with(#sk, :gid)"),
		ExpressionAttributeNames: map[string]string{
			"#sk": "groupId#memberArn",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":aid": &types.AttributeValueMemberS{Value: accountID},
			":gid": &types.AttributeValueMemberS{Value: groupID + "#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list group members: %w", err)
	}

	members := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		var member GroupMember
		if err := attributevalue.UnmarshalMap(item, &member); err != nil {
			return nil, fmt.Errorf("failed to unmarshal member: %w", err)
		}
		members = append(members, member.MemberARN)
	}

	return members, nil
}

// GetUserGroups returns all groups that a user belongs to
// Uses the member-groups-index GSI
func (s *MemberStore) GetUserGroups(ctx context.Context, accountID, memberARN string) ([]string, error) {
	result, err := s.dynamoClient.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tableName),
		IndexName:              aws.String("member-groups-index"),
		KeyConditionExpression: aws.String("#pk = :pk"),
		ExpressionAttributeNames: map[string]string{
			"#pk": "accountId#memberArn",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("%s#%s", accountID, memberARN)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get user groups: %w", err)
	}

	groups := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		var member GroupMember
		if err := attributevalue.UnmarshalMap(item, &member); err != nil {
			return nil, fmt.Errorf("failed to unmarshal member: %w", err)
		}
		groups = append(groups, member.GroupID)
	}

	return groups, nil
}

// IsMember checks if a user is a member of a group
func (s *MemberStore) IsMember(ctx context.Context, accountID, groupID, memberARN string) (bool, error) {
	result, err := s.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"accountId":         &types.AttributeValueMemberS{Value: accountID},
			"groupId#memberArn": &types.AttributeValueMemberS{Value: fmt.Sprintf("%s#%s", groupID, memberARN)},
		},
		ProjectionExpression: aws.String("accountId"),
	})
	if err != nil {
		return false, fmt.Errorf("failed to check membership: %w", err)
	}

	return result.Item != nil, nil
}

// RemoveAllGroupMembers removes all members from a group (used when deleting a group)
func (s *MemberStore) RemoveAllGroupMembers(ctx context.Context, accountID, groupID string) error {
	members, err := s.ListGroupMembers(ctx, accountID, groupID)
	if err != nil {
		return err
	}

	for _, memberARN := range members {
		if err := s.Remove(ctx, accountID, groupID, memberARN); err != nil {
			return err
		}
	}

	return nil
}
