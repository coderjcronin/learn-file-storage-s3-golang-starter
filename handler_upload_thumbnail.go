package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to prase form file", err)
		return
	}
	mediaType := header.Header.Get("Content-Type")
	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse image data", err)
		return
	}
	videoMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to lookup video by ID", err)
		return
	}

	var fileExt string

	fileExt, valid := strings.CutPrefix(mediaType, "image/")
	if !valid {
		respondWithError(w, http.StatusBadRequest, "Invalid file type, must be image", nil)
		return
	}

	vidByte := make([]byte, 32)
	rand.Read(vidByte)
	vidName := base64.RawURLEncoding.EncodeToString(vidByte)
	imageFileName := fmt.Sprintf("%s.%s", vidName, fileExt)
	imageFilePath := filepath.Join(cfg.assetsRoot, imageFileName)

	imageFile, err := os.Create(imageFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail file", err)
		return
	}
	defer imageFile.Close()

	_, err = imageFile.Write(imageData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing to thumbnail file", err)
		return
	}

	thumbnailUrl := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, imageFileName)

	newVideoMetaData := database.Video{
		ID:                videoMetaData.ID,
		CreatedAt:         videoMetaData.CreatedAt,
		UpdatedAt:         videoMetaData.UpdatedAt,
		ThumbnailURL:      &thumbnailUrl,
		VideoURL:          videoMetaData.VideoURL,
		CreateVideoParams: videoMetaData.CreateVideoParams,
	}

	err = cfg.db.UpdateVideo(newVideoMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, newVideoMetaData)
}
