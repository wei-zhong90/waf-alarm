package main

import (
	"context"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

// Response is of type APIGatewayProxyResponse since we're leveraging the
// AWS Lambda Proxy Request functionality (default behavior)
//
// https://serverless.com/framework/docs/providers/aws/events/apigateway/#lambda-proxy-integration
var svc = dynamodb.New(session.New())

// Handler is our lambda handler invoked by the `lambda.Start` function call
func Handler(ctx context.Context) {
	input := &dynamodb.QueryInput{
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":v1": {
				S: aws.String("No One You Know"),
			},
		},
		KeyConditionExpression: aws.String("Artist = :v1"),
		ProjectionExpression:   aws.String("SongTitle"),
		TableName:              aws.String("Music"),
	}

}

func main() {
	lambda.Start(Handler)
}
