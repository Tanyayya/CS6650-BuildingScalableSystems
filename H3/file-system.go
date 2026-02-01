package main

import (
	"bufio"
	"fmt"
	"os"
	"time"
)

func main() {
	const iterations = 100_000

	// -------- Unbuffered --------
	f1, err := os.Create("unbuffered.txt")
	if err != nil {
		panic(err)
	}

	startUnbuffered := time.Now()
	for i := 0; i < iterations; i++ {
		_, err := f1.Write([]byte("hello world\n"))
		if err != nil {
			panic(err)
		}
	}
	f1.Close()
	unbufferedTime := time.Since(startUnbuffered)

	// -------- Buffered --------
	f2, err := os.Create("buffered.txt")
	if err != nil {
		panic(err)
	}

	writer := bufio.NewWriter(f2)

	startBuffered := time.Now()
	for i := 0; i < iterations; i++ {
		_, err := writer.WriteString("hello world\n")
		if err != nil {
			panic(err)
		}
	}
	writer.Flush()
	f2.Close()
	bufferedTime := time.Since(startBuffered)

	fmt.Println("Unbuffered write time:", unbufferedTime)
	fmt.Println("Buffered write time:  ", bufferedTime)
}
