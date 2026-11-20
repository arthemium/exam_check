package app

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

func encodePhotoPNG(t *testing.T, source image.Image, compression png.CompressionLevel) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: compression}
	if err := encoder.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func normalizedTestPhoto(t *testing.T, source image.Image) image.Image {
	t.Helper()
	data, err := normalizePhoto(encodePhotoPNG(t, source, png.DefaultCompression), 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "jpeg" || len(data) > 3<<20 {
		t.Fatalf("invalid normalized image: format=%s bytes=%d error=%v", format, len(data), err)
	}
	return decoded
}

func TestPhotoResizePreservesAspectRatioWithoutEnlarging(t *testing.T) {
	for _, sizes := range [][4]int{{4000, 200, 3200, 160}, {200, 4000, 160, 3200}, {64, 32, 64, 32}} {
		source := image.NewGray(image.Rect(0, 0, sizes[0], sizes[1]))
		photo := normalizedTestPhoto(t, source)
		if photo.Bounds().Dx() != sizes[2] || photo.Bounds().Dy() != sizes[3] {
			t.Errorf("source %dx%d produced %v", sizes[0], sizes[1], photo.Bounds())
		}
	}
}

func TestPhotoResizePreservesTextStrokeContrast(t *testing.T) {
	source := image.NewGray(image.Rect(0, 0, 4000, 200))
	draw.Draw(source, source.Bounds(), image.White, image.Point{}, draw.Src)
	for _, stroke := range []image.Rectangle{image.Rect(1000, 30, 1010, 170), image.Rect(1050, 30, 1060, 170), image.Rect(1000, 95, 1060, 105)} {
		draw.Draw(source, stroke, image.Black, image.Point{}, draw.Src)
	}
	photo := normalizedTestPhoto(t, source)
	for _, point := range []image.Point{{804, 40}, {844, 40}, {824, 80}} {
		if value := color.GrayModel.Convert(photo.At(point.X, point.Y)).(color.Gray).Y; value > 60 {
			t.Errorf("dark letter stroke lost contrast at %v: %d", point, value)
		}
	}
	if value := color.GrayModel.Convert(photo.At(824, 40)).(color.Gray).Y; value < 220 {
		t.Errorf("space between letter strokes lost contrast: %d", value)
	}
}

func TestPhotoTransparencyFlattensOntoWhite(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(source, image.Rect(24, 8, 40, 56), image.Black, image.Point{}, draw.Src)
	photo := normalizedTestPhoto(t, source)
	if value := color.GrayModel.Convert(photo.At(4, 4)).(color.Gray).Y; value < 245 {
		t.Errorf("transparent background became dark: %d", value)
	}
	if value := color.GrayModel.Convert(photo.At(32, 32)).(color.Gray).Y; value > 10 {
		t.Errorf("opaque writing was lost: %d", value)
	}
}
