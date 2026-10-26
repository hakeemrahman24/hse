package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type WorkPermitRegister struct {
	ID           int64     `json:"id"`
	PermitNumber string    `json:"permit_number"`
	Title        string    `json:"title"`
	Location     string    `json:"location"`
	WorkType     string    `json:"work_type"`
	RequestedBy  string    `json:"requested_by"`
	Status       string    `json:"status"`
	Description  string    `json:"description"`
	StartDate    time.Time `json:"start_date"`
	EndDate      time.Time `json:"end_date"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type workPermitRegisterHandlers struct {
	db *sql.DB
}

const workPermitRegisterColumns = "id, permit_number, title, location, work_type, requested_by, status, description, start_date, end_date, created_at, updated_at"

func scanWorkPermitRegister(row interface{ Scan(...any) error }) (*WorkPermitRegister, error) {
	var p WorkPermitRegister
	err := row.Scan(
		&p.ID, &p.PermitNumber, &p.Title, &p.Location, &p.WorkType, &p.RequestedBy,
		&p.Status, &p.Description, &p.StartDate, &p.EndDate, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (h *workPermitRegisterHandlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query("SELECT " + workPermitRegisterColumns + " FROM workpermitregister ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	entries := []*WorkPermitRegister{}
	for rows.Next() {
		p, err := scanWorkPermitRegister(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		entries = append(entries, p)
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *workPermitRegisterHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row := h.db.QueryRow("SELECT "+workPermitRegisterColumns+" FROM workpermitregister WHERE id = ?", id)
	p, err := scanWorkPermitRegister(row)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *workPermitRegisterHandlers) create(w http.ResponseWriter, r *http.Request) {
	var p WorkPermitRegister
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if p.Status == "" {
		p.Status = "draft"
	}
	if p.WorkType == "" {
		p.WorkType = "general"
	}
	res, err := h.db.Exec(
		`INSERT INTO workpermitregister
			(permit_number, title, location, work_type, requested_by, status, description, start_date, end_date, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		p.PermitNumber, p.Title, p.Location, p.WorkType, p.RequestedBy, p.Status, p.Description, p.StartDate, p.EndDate,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, _ := res.LastInsertId()
	row := h.db.QueryRow("SELECT "+workPermitRegisterColumns+" FROM workpermitregister WHERE id = ?", id)
	created, err := scanWorkPermitRegister(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *workPermitRegisterHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var p WorkPermitRegister
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_, err = h.db.Exec(
		`UPDATE workpermitregister SET
			permit_number = ?, title = ?, location = ?, work_type = ?, requested_by = ?,
			status = ?, description = ?, start_date = ?, end_date = ?, updated_at = NOW()
		 WHERE id = ?`,
		p.PermitNumber, p.Title, p.Location, p.WorkType, p.RequestedBy, p.Status, p.Description, p.StartDate, p.EndDate, id,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	row := h.db.QueryRow("SELECT "+workPermitRegisterColumns+" FROM workpermitregister WHERE id = ?", id)
	updated, err := scanWorkPermitRegister(row)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *workPermitRegisterHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if _, err := h.db.Exec("DELETE FROM workpermitregister WHERE id = ?", id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
