package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	initDB()
	initStorage()

	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", healthHandler)

	// Albums
	mux.HandleFunc("PUT /albums/{album_id}", putAlbumHandler)
	mux.HandleFunc("GET /albums/{album_id}", getAlbumHandler)
	mux.HandleFunc("GET /albums", listAlbumsHandler)

	// Photos
	mux.HandleFunc("POST /albums/{album_id}/photos", uploadPhotoHandler)
	mux.HandleFunc("GET /albums/{album_id}/photos/{photo_id}", getPhotoHandler)
	mux.HandleFunc("DELETE /albums/{album_id}/photos/{photo_id}", deletePhotoHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "80"
	}

	log.Printf("Album Store listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
