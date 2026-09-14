package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testImage makes a decodable image of the given size.
func testImage(t *testing.T, w, h int, asPNG bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	var err error
	if asPNG {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSavePhotoStoresAndRecordsDimensions(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	photo, err := a.SavePhoto(ctx, bytes.NewReader(testImage(t, 800, 600, false)))
	if err != nil {
		t.Fatalf("SavePhoto: %v", err)
	}
	if photo.ID == 0 {
		t.Fatal("no id assigned")
	}
	if photo.Width != 800 || photo.Height != 600 {
		t.Errorf("dimensions %dx%d, want 800x600", photo.Width, photo.Height)
	}
	if photo.Ext != "jpg" {
		t.Errorf("ext = %q, want jpg", photo.Ext)
	}

	path := a.photoPath(photo.SHA256, photo.Ext)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("original not on disk: %v", err)
	}
	// Spread across subdirectories so one folder does not collect thousands.
	if filepath.Base(filepath.Dir(path)) != photo.SHA256[:2] {
		t.Errorf("stored at %s, expected a hash-prefixed subdirectory", path)
	}
}

// The same image uploaded twice costs one file, and the second upload is free.
func TestSavePhotoIsContentAddressed(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	data := testImage(t, 400, 300, false)

	first, err := a.SavePhoto(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.SavePhoto(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID {
		t.Errorf("the same image produced two records (%d and %d)", first.ID, second.ID)
	}

	count := 0
	filepath.Walk(a.Paths.Photos, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.Contains(path, "renders") {
			return nil
		}
		count++
		return nil
	})
	if count != 1 {
		t.Errorf("%d files stored, want 1", count)
	}
}

// A file that is not an image must be refused before it is stored, not later
// when a TV tries to show it.
func TestSavePhotoRejectsNonImages(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	_, err := a.SavePhoto(ctx, strings.NewReader("this is not an image"))
	if err == nil {
		t.Fatal("a text file was accepted as a photo")
	}
	if err != ErrUnsupportedImage {
		t.Errorf("err = %v, want ErrUnsupportedImage", err)
	}

	entries, _ := os.ReadDir(a.Paths.Photos)
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("a rejected upload left %s behind", e.Name())
		}
	}
}

func TestSavePhotoRejectsEmptyUploads(t *testing.T) {
	a := testApp(t)
	if _, err := a.SavePhoto(context.Background(), strings.NewReader("")); err == nil {
		t.Error("an empty upload was accepted")
	}
}

// Renders are produced on demand and cached, and are genuinely smaller.
func TestPhotoRendersAreSmallerAndCached(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	photo, err := a.SavePhoto(ctx, bytes.NewReader(testImage(t, 2000, 1500, false)))
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(a.photoPath(photo.SHA256, photo.Ext))
	if err != nil {
		t.Fatal(err)
	}

	for _, size := range []PhotoSize{PhotoThumb, PhotoCard} {
		path, err := a.PhotoFile(ctx, photo.ID, size)
		if err != nil {
			t.Fatalf("%s: %v", size, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s render missing: %v", size, err)
		}
		if info.Size() >= originalInfo.Size() {
			t.Errorf("%s render is %d bytes, not smaller than the %d byte original",
				size, info.Size(), originalInfo.Size())
		}

		// The long edge should match the configured target.
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := photoDimensions[size]
		if cfg.Width != want {
			t.Errorf("%s render is %dpx wide, want %d", size, cfg.Width, want)
		}

		// Asking again must reuse the cached file rather than re-rendering.
		again, err := a.PhotoFile(ctx, photo.ID, size)
		if err != nil || again != path {
			t.Errorf("%s: second call returned %q, want the cached %q", size, again, path)
		}
	}
}

// Scaling a small image up would just make it blurry, so the original is served.
func TestSmallPhotoIsNotUpscaled(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	photo, err := a.SavePhoto(ctx, bytes.NewReader(testImage(t, 100, 80, false)))
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.PhotoFile(ctx, photo.ID, PhotoCard)
	if err != nil {
		t.Fatal(err)
	}
	if path != a.photoPath(photo.SHA256, photo.Ext) {
		t.Errorf("a 100px image was rendered to card size; the original should be served")
	}
}

func TestPhotoFullReturnsTheOriginal(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	photo, err := a.SavePhoto(ctx, bytes.NewReader(testImage(t, 1200, 900, true)))
	if err != nil {
		t.Fatal(err)
	}
	if photo.Ext != "png" {
		t.Errorf("ext = %q, want png", photo.Ext)
	}
	path, err := a.PhotoFile(ctx, photo.ID, PhotoFull)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("full size returned %q, want the original PNG", path)
	}
}

// Renders are a cache: losing them must cost nothing but time.
func TestDeletedRendersAreRebuilt(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()

	photo, err := a.SavePhoto(ctx, bytes.NewReader(testImage(t, 1600, 1200, false)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.PhotoFile(ctx, photo.ID, PhotoThumb)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}

	again, err := a.PhotoFile(ctx, photo.ID, PhotoThumb)
	if err != nil {
		t.Fatalf("render was not rebuilt: %v", err)
	}
	if _, err := os.Stat(again); err != nil {
		t.Errorf("rebuilt render missing: %v", err)
	}
}
