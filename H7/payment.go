package main

import (
	"fmt"
	"log"
	"time"
)

// MaxConcurrentPayments is the hard cap on simultaneous payment verifications.
// Models a payment processor SLA of N concurrent API calls.
const MaxConcurrentPayments = 5

// PaymentVerificationDelay simulates the 3-second payment gateway round-trip.
const PaymentVerificationDelay = 3 * time.Second

// PaymentProcessor enforces concurrency limits via a buffered channel semaphore.
//
// WHY a buffered channel instead of time.Sleep:
//
//	When a goroutine calls time.Sleep(3s), the Go runtime parks the goroutine
//	and frees the OS thread — so 10,000 goroutines can all "sleep" concurrently
//	with zero real throughput penalty. That's not a bottleneck at all.
//
//	A buffered channel semaphore actually blocks:
//	  sem <- struct{}{}   // blocks when all slots are taken
//	  <-sem               // releases a slot (always in defer)
//
//	When all 5 slots are occupied, the 6th goroutine cannot proceed —
//	it blocks on the channel send. This is the real throughput wall.
type PaymentProcessor struct {
	sem     chan struct{} // buffered channel acting as semaphore (cap = MaxConcurrentPayments)
	metrics ProcessorMetrics
}

// ProcessorMetrics tracks live payment processor statistics.
type ProcessorMetrics struct {
	Processed int64
	Failed    int64
	Active    int64
}

// NewPaymentProcessor creates a processor capped at MaxConcurrentPayments.
func NewPaymentProcessor() *PaymentProcessor {
	return &PaymentProcessor{
		sem: make(chan struct{}, MaxConcurrentPayments),
	}
}

// VerifyPayment acquires a processing slot and simulates the 3-second
// payment gateway call. Blocks until a slot is free.
func (p *PaymentProcessor) VerifyPayment(orderID string, customerID int) error {
	start := time.Now()

	// Acquire a slot — goroutines ACTUALLY BLOCK here when all 5 are busy.
	p.sem <- struct{}{}
	p.metrics.Active++

	defer func() {
		<-p.sem
		p.metrics.Active--
	}()

	waitTime := time.Since(start)
	if waitTime > 100*time.Millisecond {
		log.Printf("[payment] order=%s waited %v for a processor slot", orderID, waitTime.Round(time.Millisecond))
	}

	log.Printf("[payment] order=%s customer=%d verifying...", orderID, customerID)
	time.Sleep(PaymentVerificationDelay)

	// ~2% failure rate, like a real gateway.
	if customerID%47 == 0 {
		p.metrics.Failed++
		return fmt.Errorf("payment declined for customer %d", customerID)
	}

	p.metrics.Processed++
	log.Printf("[payment] order=%s OK (total=%v)", orderID, time.Since(start).Round(time.Millisecond))
	return nil
}

// Depth returns how many slots are currently occupied.
func (p *PaymentProcessor) Depth() int {
	return len(p.sem)
}

// Metrics returns a snapshot of processor statistics.
func (p *PaymentProcessor) Metrics() ProcessorMetrics {
	return ProcessorMetrics{
		Processed: p.metrics.Processed,
		Failed:    p.metrics.Failed,
		Active:    int64(len(p.sem)),
	}
}
