package privileged

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz/client"
)

// Checker provides privileged account checking from the database
type Checker struct {
	accountsTableName string
	dynamoClient      client.DynamoDBClient
	logger            *slog.Logger
}

// NewChecker creates a new privileged account checker
func NewChecker(accountsTableName string, dynamoClient client.DynamoDBClient, logger *slog.Logger) *Checker {
	return &Checker{
		accountsTableName: accountsTableName,
		dynamoClient:      dynamoClient,
		logger:            logger,
	}
}

// IsPrivileged checks if an account is privileged in DynamoDB
func (c *Checker) IsPrivileged(ctx context.Context, accountID string) (bool, error) {
	result, err := c.dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.accountsTableName),
		Key: map[string]types.AttributeValue{
			"accountId": &types.AttributeValueMemberS{Value: accountID},
		},
		ProjectionExpression: aws.String("privileged"),
	})
	if err != nil {
		return false, err
	}

	if result.Item == nil {
		return false, nil
	}

	var account struct {
		Privileged bool `dynamodbav:"privileged"`
	}
	if err := attributevalue.UnmarshalMap(result.Item, &account); err != nil {
		return false, err
	}

	return account.Privileged, nil
}
