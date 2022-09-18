package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/sns"
)

var svc = dynamodb.New(session.New())
var notification = sns.New(session.New())

var tablename = os.Getenv("TABLENAME")

// UnixTime is our magic type
type UnixTime struct {
	time.Time
}

// UnmarshalJSON is the method that satisfies the Unmarshaller interface
func (u *UnixTime) UnmarshalJSON(b []byte) error {
	var timestamp int64
	err := json.Unmarshal(b, &timestamp)
	if err != nil {
		return err
	}
	u.Time = time.UnixMilli(timestamp)
	return nil
}

type WAFWebACL struct {
	HTTPRequest HTTPRequest `json:"httpRequest" validate:"required" description:"The metadata about the request."`
	Timestamp   UnixTime    `json:"timestamp" validate:"required" tcodec:"unix_ms" event_time:"true" description:"The timestamp in milliseconds."`
}

type HTTPRequest struct {
	Args        string       `json:"args" description:"The HTTP Request query string."`
	ClientIP    string       `json:"clientIp" panther:"ip" description:"The IP address of the client sending the request."`
	Country     string       `` /* 145-byte string literal not displayed */
	Headers     []HTTPHeader `json:"headers" description:"The list of headers."`
	HTTPMethod  string       `json:"httpMethod" description:"The HTTP method in the request."`
	HTTPVersion string       `json:"httpVersion" description:"The HTTP version, e.g. HTTP/2.0."`
	RequestID   string       `` /* 216-byte string literal not displayed */
	URI         string       `json:"uri" description:"The URI of the request."`
}

type HTTPHeader struct {
	Name  string `json:"name" description:"The header name."`
	Value string `json:"value" description:"The header value."`
}

type customCounter struct {
	counter int
	alarmed string
}

// Response is of type APIGatewayProxyResponse since we're leveraging the
// AWS Lambda Proxy Request functionality (default behavior)
//
// https://serverless.com/framework/docs/providers/aws/events/apigateway/#lambda-proxy-integration

// Handler is our lambda handler invoked by the `lambda.Start` function call
func Handler(ctx context.Context, event events.KinesisEvent) error {
	tz, err := time.LoadLocation("Asia/Shanghai")
	countTable := make(map[string]*customCounter)
	if err != nil {
		log.Print(err)
		return err
	}
	for _, record := range event.Records {
		kinesisRecord := record.Kinesis
		dataBytes := kinesisRecord.Data
		reader := bytes.NewReader(dataBytes)

		data, _ := gzip.NewReader(reader)
		content, _ := ioutil.ReadAll(data)

		var dataText events.CloudwatchLogsData
		err := json.Unmarshal(content, &dataText)
		if err != nil {
			log.Print("error:", err)
			return err
		}

		for _, message := range dataText.LogEvents {
			// message sample can be found in this SAMPLE_EVENT.json
			var myMessage WAFWebACL
			err := json.Unmarshal([]byte(message.Message), &myMessage)
			// log.Println("Message: ", myMessage)
			if err != nil {
				log.Print("error:", err)
				return err
			}
			unixtimestamp := myMessage.Timestamp
			millitimestamp := unixtimestamp.In(tz).Format("2006-01-02T15:04:05 -07:00:00")
			sourceip := myMessage.HTTPRequest.ClientIP

			// log.Println(countTable)

			updateErr := UploadDDB(sourceip, unixtimestamp, millitimestamp, message.Message, "Unalarmed")
			if updateErr != nil {
				log.Print(updateErr)
				return updateErr
			}

			if checkExist(sourceip, countTable) {
				v := countTable[sourceip]
				if v.counter >= 5 && v.alarmed != "Alarmed" {
					snserr := Alarm(message.Message, sourceip)
					if snserr != nil {
						log.Print(snserr)
						return snserr
					}
					updateErr := UploadDDB(sourceip, unixtimestamp, millitimestamp, message.Message, "Alarmed")
					if updateErr != nil {
						log.Print(updateErr)
						return updateErr
					}
					v.alarmed = "Alarmed"
				}
				log.Println(*v)
			}
		}
	}

	return nil
}

func Alarm(message string, clientip string) error {

	var prettyJSON bytes.Buffer
	jsonerr := json.Indent(&prettyJSON, []byte(message), "", "\t")
	if jsonerr != nil {
		return jsonerr
	}

	publishInput := sns.PublishInput{
		Message:  aws.String(string(prettyJSON.Bytes())),
		Subject:  aws.String(fmt.Sprintf("WAF Alarm for frequent blocking %s", clientip)),
		TopicArn: aws.String(os.Getenv("TOPIC")),
	}
	_, err := notification.Publish(&publishInput)
	if err != nil {
		return err
	}
	return nil
}

func checkExist(clientip string, counter map[string]*customCounter) bool {
	p, ispresent := counter[clientip]
	if !ispresent {
		p := customCounter{counter: 0}
		counter[clientip] = &p
		return false
	}
	p.counter++
	return true
}

func UploadDDB(sip string, utimestamp UnixTime, messageDetail ...string) error {

	input := &dynamodb.UpdateItemInput{
		ExpressionAttributeNames: map[string]*string{
			"#FTS": aws.String("Formated_Timestamp"),
			"#ET":  aws.String("ExpireTime"),
			"#MD":  aws.String("MessageDetail"),
			"#AD":  aws.String("Alarmed"),
		},
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":fts": {
				S: aws.String(messageDetail[0]),
			},
			":et": {
				N: aws.String(fmt.Sprint(utimestamp.Add(8 * time.Hour).Unix())),
			},
			":md": {
				S: aws.String(messageDetail[1]),
			},
			":ad": {
				S: aws.String(messageDetail[2]),
			},
		},
		Key: map[string]*dynamodb.AttributeValue{
			"ClientIP": {
				S: aws.String(sip),
			},
			"Timestamp": {
				N: aws.String(fmt.Sprint(utimestamp.UnixMilli())),
			},
		},
		ReturnValues:     aws.String("ALL_NEW"),
		TableName:        aws.String(tablename),
		UpdateExpression: aws.String("SET #FTS = :fts, #ET = :et, #MD = :md, #AD = :ad"),
	}

	_, err := svc.UpdateItem(input)
	if err != nil {
		log.Println(err)
		return err
	}

	// log.Println(result)
	return nil
}

func main() {
	lambda.Start(Handler)
}
