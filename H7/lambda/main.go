package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// PaymentVerificationDelay matches the ECS worker delay for fair comparison.
const PaymentVerificationDelay = 3 * time.Second

// Order matches the Order struct in the main service.
type Order struct {
	OrderID    string    `json:"order_id"`
	CustomerID int       `json:"customer_id"`
	Status     string    `json:"status"`
	Items      []Item    `json:"items"`
	CreatedAt  time.Time `json:"created_at"`
}

type Item struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

// SNSEnvelope unwraps the SNS notification wrapper.
type SNSEnvelope struct {
	Message string `json:"Message"`
}

// handler is invoked once per SNS event. SNS delivers records one at a time
// when subscribed directly (no SQS batching).
func handler(ctx context.Context, event events.SNSEvent) error {
	for _, record := range event.Records {
		if err := processRecord(ctx, record.SNS.Message); err != nil {
			// Log but don't return error — SNS will retry twice on non-nil return.
			// For payment failures we want to discard, not retry endlessly.
			log.Printf("[lambda] record error (discarding): %v", err)
		}
	}
	return nil
}

func processRecord(ctx context.Context, body string) error {
	// The SNS message body is already unwrapped by the Lambda event source.
	// However, if the order was published through our Go service which marshals
	// the order as the SNS Message string, we parse it directly.
	var order Order
	if err := json.Unmarshal([]byte(body), &order); err != nil {
		return fmt.Errorf("unmarshal order: %w", err)
	}

	log.Printf("[lambda] processing order=%s customer=%d", order.OrderID, order.CustomerID)
	start := time.Now()

	// Simulate 3-second payment gateway call — same as ECS worker.
	time.Sleep(PaymentVerificationDelay)

	// ~2% failure rate mirrors production payment gateway behaviour.
	if order.CustomerID%47 == 0 {
		return fmt.Errorf("payment declined for customer %d (order=%s)", order.CustomerID, order.OrderID)
	}

	elapsed := time.Since(start)
	log.Printf("[lambda] order=%s verified OK in %v", order.OrderID, elapsed.Round(time.Millisecond))
	return nil
}

func main() {
	lambda.Start(handler)
}
