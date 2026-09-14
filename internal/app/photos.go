package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoding
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/image/draw"

	"github.com/davisc01/derbyandales/internal/model"
	"github.com/davisc01/derbyandales/internal/store"
)

// Photos are stored once, addressed by the hash of their contents, and resized
// on demand.
//
// Content addressing means two people photographing the same car, or a retake
// of an identical frame, cost one file rather than two. It also makes the store
// idempotent: re-uploading is free and cannot corrupt anything.

// PhotoSize names a derived rendering.
type PhotoSize string

const (
	// PhotoThumb is for the ballot grid and the check-in list.
	PhotoThumb PhotoSize = "thumb"
	// PhotoCard is for the roster display and the slideshow.
	PhotoCard PhotoSize = "card"
	// PhotoFull is the original as uploaded.
	PhotoFull PhotoSize = "full"
)

// photoDimensions are the long edge of each derived size, in pixels.
//
// Card is sized for a 1080p TV showing a grid of cars; thumb for a tablet list.
var photoDimensions = map[PhotoSize]int{
	PhotoThumb: 320,
	PhotoCard:  900,
}

// maxPhotoBytes caps an upload. A phone photo is a few megabytes; anything much
// larger is a mistake or a misuse.
const maxPhotoBytes = 25 << 20 // 25 MiB

// renderQuality trades size against fidelity. Car photos are shown large on a
// TV, so this is deliberately generous.
const renderQuality = 88

// ErrUnsupportedImage is returned for anything that is not a decodable image.
var ErrUnsupportedImage = errors.New("that file is not an image the app can read")

// SavePhoto stores an image and returns its record.
func (a *App) SavePhoto(ctx context.Context, r io.Reader) (model.Photo, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPhotoBytes+1))
	if err != nil {
		return model.Photo{}, fmt.Errorf("read upload: %w", err)
	}
	if len(data) == 0 {
		return model.Photo{}, errors.New("the upload was empty")
	}
	if len(data) > maxPhotoBytes {
		return model.Photo{}, fmt.Errorf("that image is larger than %d MB", maxPhotoBytes>>20)
	}

	// Decode before storing: an undecodable file would otherwise sit in the
	// store and fail later, on a TV, in front of everyone.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return model.Photo{}, ErrUnsupportedImage
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	ext := "jpg"
	if format == "png" {
		ext = "png"
	}

	// Already stored? Return the existing record rather than a duplicate.
	if existing, err := a.DB.PhotoByHash(ctx, hash); err == nil {
		return existing, nil
	} else if !errors.Is(err, store.ErrPhotoNotFound) {
		return model.Photo{}, err
	}

	path := a.photoPath(hash, ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return model.Photo{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return model.Photo{}, fmt.Errorf("store photo: %w", err)
	}

	photo := model.Photo{
		SHA256:    hash,
		Ext:       ext,
		Width:     cfg.Width,
		Height:    cfg.Height,
		CreatedAt: time.Now(),
	}
	return a.DB.CreatePhoto(ctx, photo)
}

// photoPath is where an original lives. Files are spread across subdirectories
// by hash prefix so no single directory ends up with thousands of entries.
func (a *App) photoPath(hash, ext string) string {
	return filepath.Join(a.Paths.Photos, hash[:2], hash+"."+ext)
}

// renderPath is where a derived size is cached.
func (a *App) renderPath(hash string, size PhotoSize) string {
	return filepath.Join(a.Paths.Renders, string(size), hash+".jpg")
}

// PhotoFile returns a path to the photo at the requested size, rendering it if
// it has not been rendered before.
//
// Renders are a cache: deleting the renders directory costs nothing but time.
func (a *App) PhotoFile(ctx context.Context, id int64, size PhotoSize) (string, error) {
	photo, err := a.DB.Photo(ctx, id)
	if err != nil {
		return "", err
	}
	original := a.photoPath(photo.SHA256, photo.Ext)

	if size == PhotoFull {
		return original, nil
	}
	target, ok := photoDimensions[size]
	if !ok {
		return original, nil
	}

	rendered := a.renderPath(photo.SHA256, size)
	if _, err := os.Stat(rendered); err == nil {
		return rendered, nil
	}

	// An image already smaller than the target is served as-is rather than
	// scaled up into a blurry copy.
	if photo.Width <= target && photo.Height <= target {
		return original, nil
	}

	if err := a.renderPhoto(original, rendered, target); err != nil {
		// A failed render is not worth failing the request over: serve the
		// original and let the browser scale it.
		a.Log.Warn("rendering photo", "id", id, "size", size, "err", err)
		return original, nil
	}
	return rendered, nil
}

// renderPhoto resizes an image so its long edge is `target` pixels.
func (a *App) renderPhoto(src, dst string, target int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	img, _, err := image.Decode(in)
	if err != nil {
		return err
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w >= h {
		h = h * target / w
		w = target
	} else {
		w = w * target / h
		h = target
	}

	out := image.NewRGBA(image.Rect(0, 0, w, h))
	// CatmullRom is slower than the alternatives and noticeably sharper, which
	// matters when the result is ten feet wide on a TV.
	draw.CatmullRom.Scale(out, out.Bounds(), img, bounds, draw.Over, nil)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Write to a temporary file and rename, so a crash mid-render cannot leave
	// a truncated image cached forever.
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, out, &jpeg.Options{Quality: renderQuality}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
