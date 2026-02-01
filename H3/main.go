package main

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

type album struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Artist string  `json:"artist"`
	Price  float64 `json:"price"`
}

// Shared state + lock
var (
	albums = map[string]album{}
	mu     sync.RWMutex
)

func getAlbums(c *gin.Context) {
	mu.RLock()
	out := make([]album, 0, len(albums))
	for _, a := range albums {
		out = append(out, a)
	}
	mu.RUnlock()

	c.JSON(http.StatusOK, out)
}

func postAlbums(c *gin.Context) {
	var newAlbum album
	if err := c.BindJSON(&newAlbum); err != nil {
		return
	}

	mu.Lock()
	albums[newAlbum.ID] = newAlbum
	mu.Unlock()

	c.JSON(http.StatusCreated, newAlbum)
}

func main() {
	r := gin.Default()
	r.GET("/albums", getAlbums)
	r.POST("/albums", postAlbums)
	r.Run(":8080")
}
