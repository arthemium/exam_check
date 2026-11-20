package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxImagePixels = 20_000_000

const (
	maxStoredPhotoSide  = 3200
	maxStoredPhotoBytes = 3 << 20
	storedPhotoQuality  = 90
)

type stagedPhoto struct {
	ID, Key string
	Size    int64
}

func normalizePhoto(raw []byte, maxBytes int64) ([]byte, error) {
	configuration, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
		return nil, apiProblem{415, "Use a readable JPEG, PNG, or WebP photo"}
	}
	if configuration.Height <= 0 || configuration.Width <= 0 || configuration.Width > maxImagePixels/configuration.Height {
		return nil, apiProblem{413, "Photo exceeds the 20 megapixel limit"}
	}
	if isAnimatedPhoto(raw, format) {
		return nil, apiProblem{415, "Use a single-frame photo"}
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, apiProblem{415, "The file is not a readable exam photo"}
	}
	if orientation := photoOrientation(raw, format); orientation > 1 {
		decoded = orientedPhoto{Image: decoded, orientation: orientation}
	}
	output := limitedPhotoBuffer{limit: min(maxBytes, maxStoredPhotoBytes)}
	if err := jpeg.Encode(&output, resizePhotoForStorage(decoded), &jpeg.Options{Quality: storedPhotoQuality}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func resizePhotoForStorage(source image.Image) image.Image {
	bounds := source.Bounds()
	scale := math.Min(1, float64(maxStoredPhotoSide)/float64(max(bounds.Dx(), bounds.Dy())))
	width := max(1, int(math.Round(float64(bounds.Dx())*scale)))
	height := max(1, int(math.Round(float64(bounds.Dy())*scale)))
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(destination, destination.Bounds(), image.White, image.Point{}, draw.Src)
	if scale == 1 {
		draw.Draw(destination, destination.Bounds(), source, bounds.Min, draw.Over)
	} else {
		draw.CatmullRom.Scale(destination, destination.Bounds(), source, bounds, draw.Over, nil)
	}
	return destination
}

type limitedPhotoBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *limitedPhotoBuffer) Write(data []byte) (int, error) {
	if int64(b.Len())+int64(len(data)) > b.limit {
		return 0, apiProblem{413, "The photo is still too large after processing. Upload a clear photo of one page."}
	}
	return b.Buffer.Write(data)
}

func (s *Server) stagePhoto(ctx context.Context, header *multipart.FileHeader) (stagedPhoto, error) {
	started := time.Now()
	file, err := header.Open()
	if err != nil {
		return stagedPhoto{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, s.Config.MaxUploadBytes+1))
	if err != nil {
		return stagedPhoto{}, err
	}
	if len(raw) == 0 || int64(len(raw)) > s.Config.MaxUploadBytes {
		return stagedPhoto{}, apiProblem{413, "Photo is empty or exceeds the upload limit"}
	}
	normalized, err := normalizePhoto(raw, s.Config.MaxPageBytes)
	if err != nil {
		contextLogger(ctx).Warn("photo_transcode_failed", "input_bytes", len(raw), "duration_ms", time.Since(started).Milliseconds())
		return stagedPhoto{}, err
	}
	if err := os.MkdirAll(s.Config.UploadDir, 0700); err != nil {
		return stagedPhoto{}, err
	}
	photo := stagedPhoto{ID: NewID(), Key: NewID() + ".jpg", Size: int64(len(normalized))}
	path := filepath.Join(s.Config.UploadDir, photo.Key)
	output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return stagedPhoto{}, err
	}
	_, writeErr := output.Write(normalized)
	if writeErr == nil {
		writeErr = output.Sync()
	}
	closeErr := output.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return stagedPhoto{}, err
	}
	contextLogger(ctx).Info("photo_staged", "photo_id", photo.ID, "input_bytes", len(raw),
		"stored_bytes", photo.Size, "duration_ms", time.Since(started).Milliseconds())
	return photo, nil
}

func (s *Server) removeStagedPhotos(photos []stagedPhoto) {
	for _, photo := range photos {
		_ = os.Remove(filepath.Join(s.Config.UploadDir, photo.Key))
	}
}

func safePhotoPath(root, key string) (string, error) {
	if key == "" || filepath.Base(key) != key || strings.ContainsAny(key, `/\`) {
		return "", apiProblem{404, "Photo not found"}
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", apiProblem{404, "Photo not found"}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, key))
	if err != nil || filepath.Dir(path) != root {
		return "", apiProblem{404, "Photo not found"}
	}
	return path, nil
}

type orientedPhoto struct {
	image.Image
	orientation uint16
}

func (p orientedPhoto) Bounds() image.Rectangle {
	bounds := p.Image.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if p.orientation >= 5 {
		width, height = height, width
	}
	return image.Rect(0, 0, width, height)
}

func (p orientedPhoto) At(x, y int) color.Color {
	bounds := p.Image.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	switch p.orientation {
	case 2:
		x = width - 1 - x
	case 3:
		x, y = width-1-x, height-1-y
	case 4:
		y = height - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, height-1-x
	case 7:
		x, y = width-1-y, height-1-x
	case 8:
		x, y = width-1-y, x
	}
	return p.Image.At(bounds.Min.X+x, bounds.Min.Y+y)
}

func isAnimatedPhoto(raw []byte, format string) bool {
	if format == "png" {
		return walkPNGChunks(raw, func(kind string, _ []byte) bool { return kind == "acTL" })
	}
	if format == "webp" {
		return walkWebPChunks(raw, func(kind string, body []byte) bool {
			return kind == "ANIM" || kind == "ANMF" || (kind == "VP8X" && len(body) > 0 && body[0]&2 != 0)
		})
	}
	return false
}

func photoOrientation(raw []byte, format string) uint16 {
	orientation := uint16(1)
	inspect := func(kind string, body []byte) bool {
		if kind == "eXIf" || kind == "EXIF" || kind == "APP1" {
			orientation = exifOrientation(body)
			return orientation != 1
		}
		return false
	}
	switch format {
	case "jpeg":
		walkJPEGSegments(raw, inspect)
	case "png":
		walkPNGChunks(raw, inspect)
	case "webp":
		walkWebPChunks(raw, inspect)
	}
	return orientation
}

func walkJPEGSegments(raw []byte, inspect func(string, []byte) bool) {
	for offset := 2; offset+4 <= len(raw); {
		if raw[offset] != 0xff {
			return
		}
		marker := raw[offset+1]
		if marker == 0xda || marker == 0xd9 {
			return
		}
		if marker == 0xff {
			offset++
			continue
		}
		length := int(binary.BigEndian.Uint16(raw[offset+2 : offset+4]))
		if length < 2 || length > len(raw)-offset-2 {
			return
		}
		if marker == 0xe1 && inspect("APP1", raw[offset+4:offset+2+length]) {
			return
		}
		offset += 2 + length
	}
}

func walkPNGChunks(raw []byte, inspect func(string, []byte) bool) bool {
	for offset := 8; offset+12 <= len(raw); {
		length := uint64(binary.BigEndian.Uint32(raw[offset : offset+4]))
		if length > uint64(len(raw)-offset-12) {
			return false
		}
		end := offset + 8 + int(length)
		if inspect(string(raw[offset+4:offset+8]), raw[offset+8:end]) {
			return true
		}
		offset = end + 4
	}
	return false
}

func walkWebPChunks(raw []byte, inspect func(string, []byte) bool) bool {
	for offset := 12; offset+8 <= len(raw); {
		length := uint64(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		if length > uint64(len(raw)-offset-8) {
			return false
		}
		end := offset + 8 + int(length)
		if inspect(string(raw[offset:offset+4]), raw[offset+8:end]) {
			return true
		}
		offset = end + int(length%2)
	}
	return false
}

func exifOrientation(data []byte) uint16 {
	data = bytes.TrimPrefix(data, []byte("Exif\x00\x00"))
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset > uint64(len(data)-2) {
		return 1
	}
	count := int(order.Uint16(data[offset : offset+2]))
	for position := int(offset) + 2; count > 0 && position+12 <= len(data); position, count = position+12, count-1 {
		entry := data[position : position+12]
		if order.Uint16(entry[:2]) == 0x112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
			value := order.Uint16(entry[8:10])
			if value >= 1 && value <= 8 {
				return value
			}
		}
	}
	return 1
}
