package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to lookup video by ID", err)
		return
	}
	if userID != videoMetaData.UserID {
		respondWithError(w, http.StatusUnauthorized, "Unable to upload, not video owner", nil)
	}

	fmt.Println("uploading video file for video", videoID, "by user", userID)
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to prase form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error parsing media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Only MP4 videos allows for upload", nil)
		return
	}

	f, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create temporary upload file", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	_, err = io.Copy(f, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing to temporary upload file", err)
		return
	}

	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing temporary video file", err)
		return
	}

	vidByte := make([]byte, 32)
	rand.Read(vidByte)
	vidName := base64.RawURLEncoding.EncodeToString(vidByte)
	videoFileName := fmt.Sprintf("%s.mp4", vidName)

	inputObject := s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(videoFileName),
		Body:        f,
		ContentType: aws.String(mediaType),
	}

	_, err = cfg.s3Client.PutObject(r.Context(), &inputObject)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading to s3 bucket", err)
		return
	}

	videoUrl := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, videoFileName)

	newVideoMetaData := database.Video{
		ID:                videoMetaData.ID,
		CreatedAt:         videoMetaData.CreatedAt,
		UpdatedAt:         videoMetaData.UpdatedAt,
		ThumbnailURL:      videoMetaData.ThumbnailURL,
		VideoURL:          &videoUrl,
		CreateVideoParams: videoMetaData.CreateVideoParams,
	}

	err = cfg.db.UpdateVideo(newVideoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, newVideoMetaData)

}
