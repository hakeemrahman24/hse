package main

import (
	"context"
	"database/sql"
	"errors"
	"mime/multipart"
)

// uploadedFiles is a batch of photos and PDFs that passed the checks and are already on disk,
// waiting for their rows. Either insert them or discard them.
type uploadedFiles struct {
	photos []storedPhoto
	docs   []storedDoc
}

// prepareUploads checks and stores the "photos" and "documents" fields of a parsed multipart form.
// have is what the observation already holds of that kind. Nothing stays on disk if it fails; the
// error text is safe to show to the user.
func prepareUploads(ctx context.Context, dir string, form *multipart.Form, have fileCounts) (uploadedFiles, error) {
	return prepareUploadsFrom(ctx, dir, form, "photos", "documents", have)
}

// prepareUploadsFrom is prepareUploads for form fields with other names. The add form has a second
// pair, "after_photos" and "after_documents", for the evidence of the action taken.
func prepareUploadsFrom(ctx context.Context, dir string, form *multipart.Form, photoField, docField string, have fileCounts) (uploadedFiles, error) {
	var u uploadedFiles
	if form == nil {
		return u, nil
	}
	var err error
	if u.photos, err = storeUploads(ctx, dir, form.File[photoField], have.photos); err != nil {
		return uploadedFiles{}, errors.New("Photo not accepted: " + err.Error())
	}
	if u.docs, err = storeDocuments(dir, form.File[docField], have.pdfs); err != nil {
		removePhotoFiles(dir, namesOf(u.photos))
		return uploadedFiles{}, errors.New("Document not accepted: " + err.Error())
	}
	return u, nil
}

func (u uploadedFiles) names() []string { return append(namesOf(u.photos), docNames(u.docs)...) }

func (u uploadedFiles) discard(dir string) { removePhotoFiles(dir, u.names()) }

// insert writes one row per file, all or none.
func (u uploadedFiles) insert(ctx context.Context, db *sql.DB, obsID int64, kind string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	add := func(fileType, name, display string, size int) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO observation_files (observation_id, kind, file_type, stored_name, display_name, size_bytes) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
			obsID, kind, fileType, name, display, size)
		return err
	}
	for _, p := range u.photos {
		if err := add(fileTypePhoto, p.Name, "", p.Size); err != nil {
			return err
		}
	}
	for _, d := range u.docs {
		if err := add(fileTypePDF, d.Name, d.Display, d.Size); err != nil {
			return err
		}
	}
	return tx.Commit()
}
