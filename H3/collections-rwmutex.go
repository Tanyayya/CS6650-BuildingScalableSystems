package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	m := make(map[int]int)
	var mu sync.RWMutex
	var wg sync.WaitGroup

	start := time.Now()

	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				key := g*1000 + i
				mu.Lock()        
				m[key] = i
				mu.Unlock()
			}
		}(g)
	}

	wg.Wait()

	elapsed := time.Since(start)
	fmt.Println("len:", len(m))
	fmt.Println("time:", elapsed)
}
