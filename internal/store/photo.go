package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/davisc01/derbyandales/internal/model"
)

// ErrPhotoNotFound is returned when no photo matches.
var ErrPhotoNotFound = errors.New("photo not found")

const photoCols = `id, sha256, ext, width, height, created_at`

func scanPhoto(sc interface{ Scan(...any) error }) (model.Photo, error) {
	var p model.Photo
	var created int64
	err := sc.Scan(&p.ID, &p.SHA256, &p.Ext, &p.Width, &p.Height, &created)
	if err != nil {
		return p, err
	}
	p.CreatedAt = fromUnix(created)
	return p, nil
}

// CreatePhoto records a stored image.
func (db *DB) CreatePhoto(ctx context.Context, p model.Photo) (model.Photo, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO photo (sha256, ext, width, height, created_at) VALUES (?,?,?,?,?)`,
		p.SHA256, p.Ext, p.Width, p.Height, unix(p.CreatedAt))
	if err != nil {
		return p, fmt.Errorf("insert photo: %w", err)
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

// Photo loads one photo by id.
func (db *DB) Photo(ctx context.Context, id int64) (model.Photo, error) {
	row := db.QueryRowContext(ctx, `SELECT `+photoCols+` FROM photo WHERE id = ?`, id)
	p, err := scanPhoto(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrPhotoNotFound
	}
	return p, err
}

// PhotoByHash finds an already-stored photo by its content hash, which is how
// re-uploading the same image costs nothing.
func (db *DB) PhotoByHash(ctx context.Context, hash string) (model.Photo, error) {
	row := db.QueryRowContext(ctx, `SELECT `+photoCols+` FROM photo WHERE sha256 = ?`, hash)
	p, err := scanPhoto(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrPhotoNotFound
	}
	return p, err
}

// SetEntryPhoto attaches a photo to a car.
func (db *DB) SetEntryPhoto(ctx context.Context, entryID, photoID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE entry SET photo_id = ? WHERE id = ?`, photoID, entryID)
	return err
}
