package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/sns"
)

// Response is of type APIGatewayProxyResponse since we're leveraging the
// AWS Lambda Proxy Request functionality (default behavior)
//
// https://serverless.com/framework/docs/providers/aws/events/apigateway/#lambda-proxy-integration
var svc = dynamodb.New(session.New())
var IPtablename = os.Getenv("IPTABLENAME")
var tablename = os.Getenv("TABLENAME")
var notification = sns.New(session.New())

type sendMessage struct {
	ClientIp        *string
	FormatTimestamp *string
	Detail          *string
}

// Handler is our lambda handler invoked by the `lambda.Start` function call
func Handler(ctx context.Context) {
	var alarmMessageList []sendMessage
	result, err := GetIpList()
	if err != nil {
		log.Fatal(err)
	}

	for _, ip := range result {
		alarmlist := []*string{}

		input := &dynamodb.QueryInput{
			ExpressionAttributeNames: map[string]*string{
				"#TS": aws.String("Timestamp"),
			},
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":ip": {
					S: ip,
				},
				":start": {
					N: aws.String(fmt.Sprint(time.Now().Add(-time.Minute * 5).UnixMilli())),
				},
				":end": {
					N: aws.String(fmt.Sprint(time.Now().UnixMilli())),
				},
			},
			KeyConditionExpression: aws.String("ClientIP = :ip AND #TS BETWEEN :start AND :end"),
			ProjectionExpression:   aws.String("Formated_Timestamp,MessageDetail,Alarmed"),
			TableName:              aws.String(tablename),
		}

		output, err := svc.Query(input)
		if err != nil {
			log.Fatal(err)
		}

		if len(output.Items) >= 3 {
			for _, item := range output.Items {
				if item["Alarmed"].S == aws.String("Alarmed") {
					continue
				}
				alarmlist = append(alarmlist, item["Alarmed"].S)
			}
			if len(alarmlist) == len(output.Items) {
				alarmMessageList = append(alarmMessageList, sendMessage{
					ClientIp:        ip,
					FormatTimestamp: output.Items[0]["Formated_Timestamp"].S,
					Detail:          output.Items[0]["MessageDetail"].S,
				})
			}

		}
	}

	log.Print(alarmMessageList)

	if len(alarmMessageList) > 0 {
		Alarm(alarmMessageList)
	}

}

func Alarm(message []sendMessage) error {
	for _, alarmmessage := range message {
		var prettyJSON bytes.Buffer
		jsonerr := json.Indent(&prettyJSON, []byte(*alarmmessage.Detail), "", "\t")
		if jsonerr != nil {
			return jsonerr
		}

		publishInput := sns.PublishInput{
			Message:  aws.String(string(prettyJSON.Bytes())),
			Subject:  aws.String(fmt.Sprintf("WAF Alarm for frequent blocking %s at %s", *alarmmessage.ClientIp, *alarmmessage.FormatTimestamp)),
			TopicArn: aws.String(os.Getenv("TOPIC")),
		}
		_, err := notification.Publish(&publishInput)
		if err != nil {
			return err
		}
	}
	return nil
}

func GetIpList() ([]*string, error) {
	var ipList []*string
	input := &dynamodb.ScanInput{
		ExpressionAttributeNames: map[string]*string{
			"#IP": aws.String("ClientIP"),
		},
		ProjectionExpression: aws.String("#IP"),
		TableName:            aws.String(IPtablename),
	}

	result, err := svc.Scan(input)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	for _, ip := range result.Items {
		ipList = append(ipList, ip["ClientIP"].S)
	}
	return ipList, nil
}

func main() {
	lambda.Start(Handler)
}
