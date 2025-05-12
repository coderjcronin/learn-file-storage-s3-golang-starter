package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

type ffmpegOutput struct {
	Streams []struct {
		Index              int    `json:"index"`
		CodecName          string `json:"codec_name,omitempty"`
		CodecLongName      string `json:"codec_long_name,omitempty"`
		Profile            string `json:"profile,omitempty"`
		CodecType          string `json:"codec_type"`
		CodecTagString     string `json:"codec_tag_string"`
		CodecTag           string `json:"codec_tag"`
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
		CodedWidth         int    `json:"coded_width,omitempty"`
		CodedHeight        int    `json:"coded_height,omitempty"`
		ClosedCaptions     int    `json:"closed_captions,omitempty"`
		FilmGrain          int    `json:"film_grain,omitempty"`
		HasBFrames         int    `json:"has_b_frames,omitempty"`
		SampleAspectRatio  string `json:"sample_aspect_ratio,omitempty"`
		DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
		PixFmt             string `json:"pix_fmt,omitempty"`
		Level              int    `json:"level,omitempty"`
		ColorRange         string `json:"color_range,omitempty"`
		ColorSpace         string `json:"color_space,omitempty"`
		ColorTransfer      string `json:"color_transfer,omitempty"`
		ColorPrimaries     string `json:"color_primaries,omitempty"`
		ChromaLocation     string `json:"chroma_location,omitempty"`
		FieldOrder         string `json:"field_order,omitempty"`
		Refs               int    `json:"refs,omitempty"`
		IsAvc              string `json:"is_avc,omitempty"`
		NalLengthSize      string `json:"nal_length_size,omitempty"`
		ID                 string `json:"id"`
		RFrameRate         string `json:"r_frame_rate"`
		AvgFrameRate       string `json:"avg_frame_rate"`
		TimeBase           string `json:"time_base"`
		StartPts           int    `json:"start_pts"`
		StartTime          string `json:"start_time"`
		DurationTs         int    `json:"duration_ts"`
		Duration           string `json:"duration"`
		BitRate            string `json:"bit_rate,omitempty"`
		BitsPerRawSample   string `json:"bits_per_raw_sample,omitempty"`
		NbFrames           string `json:"nb_frames"`
		ExtradataSize      int    `json:"extradata_size"`
		Disposition        struct {
			Default         int `json:"default"`
			Dub             int `json:"dub"`
			Original        int `json:"original"`
			Comment         int `json:"comment"`
			Lyrics          int `json:"lyrics"`
			Karaoke         int `json:"karaoke"`
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
			VisualImpaired  int `json:"visual_impaired"`
			CleanEffects    int `json:"clean_effects"`
			AttachedPic     int `json:"attached_pic"`
			TimedThumbnails int `json:"timed_thumbnails"`
			NonDiegetic     int `json:"non_diegetic"`
			Captions        int `json:"captions"`
			Descriptions    int `json:"descriptions"`
			Metadata        int `json:"metadata"`
			Dependent       int `json:"dependent"`
			StillImage      int `json:"still_image"`
		} `json:"disposition"`
		Tags struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
			Encoder     string `json:"encoder"`
			Timecode    string `json:"timecode"`
		} `json:"tags"`
		SampleFmt      string `json:"sample_fmt,omitempty"`
		SampleRate     string `json:"sample_rate,omitempty"`
		Channels       int    `json:"channels,omitempty"`
		ChannelLayout  string `json:"channel_layout,omitempty"`
		BitsPerSample  int    `json:"bits_per_sample,omitempty"`
		InitialPadding int    `json:"initial_padding,omitempty"`
	} `json:"streams"`
}

func processVideoForFastStart(filePath string) (string, error) {
	resultFile := fmt.Sprintf("%s.processing", filePath)
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", resultFile)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return resultFile, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	// Create a buffer to capture stdout
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// Run the command
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffprobe error: %w", err)
	}

	// Unmarshal the JSON output
	var ffmpegOut ffmpegOutput
	if err := json.Unmarshal(stdout.Bytes(), &ffmpegOut); err != nil {
		return "", fmt.Errorf("json unmarshal error: %w", err)
	}

	// Check if we have video streams
	if len(ffmpegOut.Streams) == 0 {
		return "", fmt.Errorf("no streams found in video")
	}

	switch ffmpegOut.Streams[0].DisplayAspectRatio {
	case "":
		if ffmpegOut.Streams[0].Height == 0 || ffmpegOut.Streams[0].Width == 0 {
			return "", fmt.Errorf("invalid video width (%d) or height (%d)", ffmpegOut.Streams[0].Width, ffmpegOut.Streams[0].Height)
		}
		var ratio float32 = float32(ffmpegOut.Streams[0].Height) / float32(ffmpegOut.Streams[0].Width)
		if ratio < 1 && ratio > 0.45 {
			return "portrait", nil
		} else if ratio > 1 && ratio < 2 {
			return "landscape", nil
		}
		return "other", nil
	case "16:9":
		return "landscape", nil
	case "9:16":
		return "portrait", nil
	default:
		return "other", nil
	}
}

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

	filePath, err := filepath.Abs(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error finding temp file path", err)
		return
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Println("File does not exist at:", filePath)
		respondWithError(w, http.StatusInternalServerError, "Could not find temp file", err)
		return
	}

	ffmpegResult, err := getVideoAspectRatio(filePath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Error determining aspect ratio", err)
		return
	}

	finalFilePath, err := processVideoForFastStart(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing file for fast play", err)
		return
	}

	finalFile, err := os.Open(finalFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening processed file", err)
		return
	}
	defer finalFile.Close()

	vidByte := make([]byte, 32)
	rand.Read(vidByte)
	vidName := base64.RawURLEncoding.EncodeToString(vidByte)
	videoFileName := fmt.Sprintf("%s/%s.mp4", ffmpegResult, vidName)

	inputObject := s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(videoFileName),
		Body:        finalFile,
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
