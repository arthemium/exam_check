package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestPhotoNormalizeRejectsHugeDimensionsBeforeDecoding(t *testing.T) {
	raw := testPNG(t)
	binary.BigEndian.PutUint32(raw[16:20], 5001)
	binary.BigEndian.PutUint32(raw[20:24], 4000)
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	_, err := normalizePhoto(raw, 10<<20)
	var problem apiProblem
	if !errors.As(err, &problem) || problem.status != 413 {
		t.Fatalf("expected dimension limit, got %v", err)
	}
}

func TestPhotoNormalizeAppliesEXIFAndStripsMetadata(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 24, 12)), nil); err != nil {
		t.Fatal(err)
	}
	exif := append([]byte("Exif\x00\x00"), []byte{
		'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0,
		0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0,
	}...)
	segment := []byte{0xff, 0xe1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}
	segment = append(segment, exif...)
	raw := append([]byte{0xff, 0xd8}, segment...)
	raw = append(raw, encoded.Bytes()[2:]...)
	output, err := normalizePhoto(raw, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	configuration, _, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil || configuration.Width != 12 || configuration.Height != 24 {
		t.Fatalf("unexpected rotated dimensions: %+v %v", configuration, err)
	}
	if bytes.Contains(output, []byte("Exif")) {
		t.Fatal("EXIF metadata was preserved")
	}
}

func TestPhotoAllEXIFOrientationsUseExpectedPixels(t *testing.T) {
	photo := image.NewNRGBA(image.Rect(4, 7, 6, 10))
	for y := range 3 {
		for x := range 2 {
			photo.SetNRGBA(x+4, y+7, color.NRGBA{R: byte('a' + y*2 + x), A: 255})
		}
	}
	expected := []string{"abcdef", "badcfe", "fedcba", "efcdab", "acebdf", "ecafdb", "fdbeca", "bdface"}
	for index, want := range expected {
		view := orientedPhoto{Image: photo, orientation: uint16(index + 1)}
		var values []byte
		for y := range view.Bounds().Dy() {
			for x := range view.Bounds().Dx() {
				red, _, _, _ := view.At(x, y).RGBA()
				values = append(values, byte(red>>8))
			}
		}
		if string(values) != want {
			t.Errorf("orientation %d: got %s, want %s", index+1, values, want)
		}
	}
}

func TestPhotoAnimationAndOutputLimits(t *testing.T) {
	raw := testPNG(t)
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk[:4], 8)
	copy(chunk[4:8], "acTL")
	binary.BigEndian.PutUint32(chunk[8:12], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	animated := append(append(append([]byte{}, raw[:33]...), chunk...), raw[33:]...)
	_, err := normalizePhoto(animated, 10<<20)
	var problem apiProblem
	if !errors.As(err, &problem) || problem.status != 415 {
		t.Fatalf("animated PNG accepted: %v", err)
	}
	_, err = normalizePhoto(raw, 10)
	if !errors.As(err, &problem) || problem.status != 413 {
		t.Fatalf("encoded output cap ignored: %v", err)
	}
}

func TestPhotoWebPIsDecodedAndNormalized(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString("UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoEAAMAAUAmJaQAA3AA/vz0AAA=")
	if err != nil {
		t.Fatal(err)
	}
	output, err := normalizePhoto(raw, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(output))
	if err != nil || format != "jpeg" || configuration.Width != 4 || configuration.Height != 3 {
		t.Fatalf("WebP normalization failed: %+v %s %v", configuration, format, err)
	}
}

func TestPhotoPathsRejectTraversalAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "symlink.jpg")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../secret.jpg", "nested/photo.jpg", `nested\photo.jpg`, "symlink.jpg"} {
		if _, err := safePhotoPath(root, key); err == nil {
			t.Errorf("unsafe key accepted: %s", key)
		}
	}
}

func FuzzPhotoMetadata(f *testing.F) {
	f.Add([]byte("Exif\x00\x00II*\x00\xff\xff\xff\xff"))
	f.Add([]byte("RIFF\x10\x00\x00\x00WEBP"))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, format := range []string{"jpeg", "png", "webp"} {
			orientation := photoOrientation(data, format)
			if orientation < 1 || orientation > 8 {
				t.Fatal("invalid orientation")
			}
			_ = isAnimatedPhoto(data, format)
		}
	})
}
