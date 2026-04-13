package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// ── models ───────────────────────────────────────────────────────────────────

type Album struct {
	AlbumID     string `json:"album_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Owner       string `json:"owner"`
}

type Photo struct {
	PhotoID string  `json:"photo_id"`
	AlbumID string  `json:"album_id"`
	Seq     int     `json:"seq"`
	Status  string  `json:"status"`
	URL     *string `json:"url,omitempty"`
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func pathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

// ddbStr safely extracts a string attribute from a DynamoDB item.
func ddbStr(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

// ddbNum safely extracts a numeric attribute from a DynamoDB item.
func ddbNum(item map[string]types.AttributeValue, key string) int {
	if v, ok := item[key].(*types.AttributeValueMemberN); ok {
		n, _ := strconv.Atoi(v.Value)
		return n
	}
	return 0
}

// ── handlers ─────────────────────────────────────────────────────────────────

// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PUT /albums/{album_id}
func putAlbumHandler(w http.ResponseWriter, r *http.Request) {
	albumID := pathParam(r, "album_id")

	var body Album
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	ctx := r.Context()

	// UpdateItem upserts metadata fields without touching seq_counter.
	// ReturnValues=ALL_OLD is empty when the item did not previously exist.
	updateOut, err := ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(albumsTable),
		Key: map[string]types.AttributeValue{
			"album_id": &types.AttributeValueMemberS{Value: albumID},
		},
		UpdateExpression: aws.String("SET title = :t, description = :d, #ow = :o"),
		ExpressionAttributeNames: map[string]string{
			"#ow": "owner",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t": &types.AttributeValueMemberS{Value: body.Title},
			":d": &types.AttributeValueMemberS{Value: body.Description},
			":o": &types.AttributeValueMemberS{Value: body.Owner},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		log.Printf("ERROR [putAlbum]: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	album := Album{AlbumID: albumID, Title: body.Title, Description: body.Description, Owner: body.Owner}
	statusCode := http.StatusOK
	if len(updateOut.Attributes) == 0 {
		statusCode = http.StatusCreated
	}
	writeJSON(w, statusCode, album)
}

// GET /albums/{album_id}
func getAlbumHandler(w http.ResponseWriter, r *http.Request) {
	albumID := pathParam(r, "album_id")

	out, err := ddb.GetItem(r.Context(), &dynamodb.GetItemInput{
		TableName: aws.String(albumsTable),
		Key: map[string]types.AttributeValue{
			"album_id": &types.AttributeValueMemberS{Value: albumID},
		},
	})
	if err != nil {
		log.Printf("ERROR [getAlbum]: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if out.Item == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	a := Album{
		AlbumID:     ddbStr(out.Item, "album_id"),
		Title:       ddbStr(out.Item, "title"),
		Description: ddbStr(out.Item, "description"),
		Owner:       ddbStr(out.Item, "owner"),
	}
	writeJSON(w, http.StatusOK, a)
}

// GET /albums
func listAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	albums := make([]Album, 0)
	var lastKey map[string]types.AttributeValue

	for {
		out, err := ddb.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(albumsTable),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			log.Printf("ERROR [listAlbums]: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		for _, item := range out.Items {
			albums = append(albums, Album{
				AlbumID:     ddbStr(item, "album_id"),
				Title:       ddbStr(item, "title"),
				Description: ddbStr(item, "description"),
				Owner:       ddbStr(item, "owner"),
			})
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	writeJSON(w, http.StatusOK, albums)
}

// POST /albums/{album_id}/photos
func uploadPhotoHandler(w http.ResponseWriter, r *http.Request) {
	albumID := pathParam(r, "album_id")
	ctx := r.Context()

	// Verify album exists.
	getOut, err := ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(albumsTable),
		Key: map[string]types.AttributeValue{
			"album_id": &types.AttributeValueMemberS{Value: albumID},
		},
	})
	if err != nil {
		log.Printf("ERROR [uploadPhoto] GetItem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if getOut.Item == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	// ParseMultipartForm buffers the entire request body so the file handle
	// stays valid after we return the 202 — actual io.ReadAll runs in the goroutine.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart"})
		return
	}
	var photoFile multipart.File
	contentType := "image/jpeg"
	if f, header, ferr := r.FormFile("photo"); ferr == nil {
		photoFile = f
		if ct := header.Header.Get("Content-Type"); ct != "" {
			contentType = ct
		}
	}

	// Atomically increment seq_counter on the album record.
	counterOut, err := ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(albumsTable),
		Key: map[string]types.AttributeValue{
			"album_id": &types.AttributeValueMemberS{Value: albumID},
		},
		UpdateExpression: aws.String("ADD seq_counter :one"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		log.Printf("ERROR [uploadPhoto] counter: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "counter error"})
		return
	}
	seq := ddbNum(counterOut.Attributes, "seq_counter")

	photoID := uuid.New().String()

	// Persist photo record with status=processing.
	_, err = ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(photosTable),
		Item: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
			"album_id": &types.AttributeValueMemberS{Value: albumID},
			"seq":      &types.AttributeValueMemberN{Value: strconv.Itoa(seq)},
			"status":   &types.AttributeValueMemberS{Value: "processing"},
		},
	})
	if err != nil {
		log.Printf("ERROR [uploadPhoto] PutItem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// 202 returned here — everything below is async.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"photo_id": photoID,
		"seq":      seq,
		"status":   "processing",
	})

	go func() {
		var data []byte
		if photoFile != nil {
			var readErr error
			data, readErr = io.ReadAll(photoFile)
			photoFile.Close()
			if readErr != nil {
				log.Printf("ERROR [uploadPhoto] read file: %v", readErr)
				markPhotoFailed(photoID)
				return
			}
		}
		if err := processPhoto(context.Background(), photoID, albumID, data, contentType); err != nil {
			log.Printf("ERROR processPhoto %s: %v", photoID, err)
			markPhotoFailed(photoID)
		}
	}()
}

func markPhotoFailed(photoID string) {
	ddb.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{ //nolint:errcheck
		TableName: aws.String(photosTable),
		Key: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
		},
		UpdateExpression: aws.String("SET #s = :failed"),
		ExpressionAttributeNames: map[string]string{"#s": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":failed": &types.AttributeValueMemberS{Value: "failed"},
		},
	})
}

// GET /albums/{album_id}/photos/{photo_id}
func getPhotoHandler(w http.ResponseWriter, r *http.Request) {
	albumID := pathParam(r, "album_id")
	photoID := pathParam(r, "photo_id")

	out, err := ddb.GetItem(r.Context(), &dynamodb.GetItemInput{
		TableName: aws.String(photosTable),
		Key: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
		},
	})
	if err != nil {
		log.Printf("ERROR [getPhoto]: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if out.Item == nil || ddbStr(out.Item, "album_id") != albumID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	p := Photo{
		PhotoID: ddbStr(out.Item, "photo_id"),
		AlbumID: ddbStr(out.Item, "album_id"),
		Seq:     ddbNum(out.Item, "seq"),
		Status:  ddbStr(out.Item, "status"),
	}
	if u := ddbStr(out.Item, "url"); u != "" && p.Status == "completed" {
		p.URL = &u
	}
	writeJSON(w, http.StatusOK, p)
}

// DELETE /albums/{album_id}/photos/{photo_id}
func deletePhotoHandler(w http.ResponseWriter, r *http.Request) {
	albumID := pathParam(r, "album_id")
	photoID := pathParam(r, "photo_id")

	out, err := ddb.GetItem(r.Context(), &dynamodb.GetItemInput{
		TableName: aws.String(photosTable),
		Key: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
		},
	})
	if err != nil {
		log.Printf("ERROR [deletePhoto] GetItem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if out.Item == nil || ddbStr(out.Item, "album_id") != albumID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if fileKey := ddbStr(out.Item, "file_key"); fileKey != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if err := deleteFile(ctx, fileKey); err != nil {
			log.Printf("WARN [deletePhoto] s3 delete: %v", err)
		}
	}

	_, err = ddb.DeleteItem(r.Context(), &dynamodb.DeleteItemInput{
		TableName: aws.String(photosTable),
		Key: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
		},
	})
	if err != nil {
		log.Printf("ERROR [deletePhoto] DeleteItem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// processPhoto uploads to S3 then marks the photo completed in DynamoDB.
func processPhoto(ctx context.Context, photoID, albumID string, data []byte, contentType string) error {
	if data == nil {
		data = minimalJPEG()
		contentType = "image/jpeg"
	}

	key := fmt.Sprintf("photos/%s/%s", albumID, photoID)
	s3url, err := uploadToS3(ctx, key, contentType, data)
	if err != nil {
		return err
	}

	_, err = ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(photosTable),
		Key: map[string]types.AttributeValue{
			"photo_id": &types.AttributeValueMemberS{Value: photoID},
		},
		UpdateExpression: aws.String("SET #s = :completed, #u = :url, file_key = :fk"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
			"#u": "url",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":completed": &types.AttributeValueMemberS{Value: "completed"},
			":url":       &types.AttributeValueMemberS{Value: s3url},
			":fk":        &types.AttributeValueMemberS{Value: key},
		},
	})
	return err
}
