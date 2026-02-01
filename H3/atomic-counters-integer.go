package main

import (
	"fmt"
	"runtime"
	"sync"
)

func main() {
	runtime.GOMAXPROCS(8)

	var ops int
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				ops++ // NOT SAFE
			}
		}()
	}

	wg.Wait()
	fmt.Println("ops:", ops)
}
