package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	// Let Go use multiple OS threads (one per logical CPU).
	runtime.GOMAXPROCS(runtime.NumCPU())

	const roundTrips = 1_000_000
	ch := make(chan struct{})

	start := time.Now()

	go func() {
		for i := 0; i < roundTrips; i++ {
			<-ch
			ch <- struct{}{}
		}
	}()

	for i := 0; i < roundTrips; i++ {
		ch <- struct{}{}
		<-ch
	}

	elapsed := time.Since(start)
	avgSwitch := elapsed / time.Duration(2*roundTrips)

	fmt.Println("GOMAXPROCS:", runtime.GOMAXPROCS(0))
	fmt.Println("Total duration:", elapsed)
	fmt.Println("Average switch time:", avgSwitch)
}
