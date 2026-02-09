package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	runtime.GOMAXPROCS(1)

	const roundTrips = 1_000_000
	ch := make(chan struct{}) //transferring the signal

	start := time.Now()

	// Goroutine A: receives then sends (ping-pong partner)
	go func() {
		for i := 0; i < roundTrips; i++ {
			<-ch
			ch <- struct{}{}
		}
	}()

	// Goroutine B (main): sends then receives
	for i := 0; i < roundTrips; i++ {
		ch <- struct{}{}
		<-ch
	}

	elapsed := time.Since(start)

	// Each round trip has 2 hand-offs (send+recv unblock the other goroutine).
	// Prompt says divide by twice the number of round-trips.
	avgSwitch := elapsed / time.Duration(2*roundTrips)

	fmt.Println("Total duration:", elapsed)
	fmt.Println("Average switch time:", avgSwitch)
}
