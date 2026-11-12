// Package permitlog reads stored work entry permits.
//
// WHY separate from package permit: permit holds pure rules that never touch
// a database (slice 2). This package is the small database side. Slice 3b only
// needs "what is this person's latest permit?" for the duplicate gate; slice 3c
// adds saving a new one.
package permitlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
)

// Store reads the permit table.
type Store struct{ DB *sql.DB }

// Found is the latest earlier permit plus the ids shown in a "blocked" message.
type Found struct {
	Previous  permit.Previous
	RequestID string
	LepNo     string
	Prefill   Prefill
}

// Prefill is what a renewal offers again (business rules section 4): phone,
// address and emergency contact. Identity, company and position come from medpost.
type Prefill struct {
	Phone, EmergencyName, EmergencyPhone         string
	Province, Regency, District, Village, Street string
}

// Latest returns the person's most recent permit, matched on the medpost
// patient id or, when medpost supplies one, the NIK / passport number (plan
// 4.1: PersonalID is the long-term person key; patientId covers the time before
// medpost adds it to the employees route). Exists is false when there is none.
func (s Store) Latest(ctx context.Context, patientID int64, personalID string) (Found, error) {
	var f Found
	var status, company, mcu, visa, lep string
	err := s.DB.QueryRowContext(ctx, `
SELECT request_id, lep_no, status, company, mcu_valid_until, visa_expiry, lep_valid_until,
       phone, emergency_name, emergency_phone, province, regency, district, village, street
FROM permit
WHERE patient_id = ? OR (? <> '' AND personal_id = ?)
ORDER BY created_at DESC, request_id DESC
LIMIT 1`, patientID, personalID, personalID).
		Scan(&f.RequestID, &f.LepNo, &status, &company, &mcu, &visa, &lep,
			&f.Prefill.Phone, &f.Prefill.EmergencyName, &f.Prefill.EmergencyPhone,
			&f.Prefill.Province, &f.Prefill.Regency, &f.Prefill.District, &f.Prefill.Village, &f.Prefill.Street)
	if errors.Is(err, sql.ErrNoRows) {
		return Found{}, nil
	}
	if err != nil {
		return Found{}, fmt.Errorf("read latest permit: %w", err)
	}
	f.Previous = permit.Previous{
		Exists: true, Status: status, Company: company,
		McuValidUntil: dates.Day(mcu), VisaExpiry: dates.Day(visa), LepValidUntil: dates.Day(lep),
	}
	return f, nil
}

// Adopt links permits that belong to no medpost patient yet (patient_id 0, as the spreadsheet
// import leaves them) to the worker medpost has just confirmed, and returns how many it linked.
// A permit is taken only when the name (any case, any spacing) and the birth date both match.
// WHY here and then: this runs at the shared verified lookup, so every later query that matches on
// the patient id (the My permit list, the duplicate gate, the renewals) finds the permit with no
// change of its own. The personal id (NIK or passport) is the long-term key (plan 4.1): when
// medpost sends one it is stamped on a permit that has none, a permit holding a different one is
// somebody else and stays unlinked, and a permit keeps the one from the sheet when medpost sends
// none. Never matches on a name alone: a blank birth date or name links nothing.
func (s Store) Adopt(ctx context.Context, patientID int64, personalID, name, dob string) (int, error) {
	name = strings.ToLower(strings.Join(strings.Fields(name), " "))
	if patientID <= 0 || name == "" || dob == "" {
		return 0, nil
	}
	res, err := s.DB.ExecContext(ctx, `
UPDATE permit
SET patient_id = ?, personal_id = CASE WHEN personal_id = '' THEN ? ELSE personal_id END
WHERE patient_id = 0 AND dob = ? AND LOWER(TRIM(REPLACE(worker_name, '  ', ' '))) = ?
  AND (personal_id = '' OR ? = '' OR personal_id = ?)`,
		patientID, personalID, dob, name, personalID, personalID)
	if err != nil {
		return 0, fmt.Errorf("adopt permits: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("adopt permits: %w", err)
	}
	return int(n), nil
}

// NewPermit is one submitted request. The medpost fields are a snapshot taken
// at submit: Final approval uses them, so a re-check-up in medpost between the
// two steps cannot change a permit mid-review (business rules section 3).
type NewPermit struct {
	RequestID, Status                                            string
	LepNo                                                        string // blank until Final approval; the importer sets it for permits that already have one
	PatientID                                                    int64
	PersonalID, WorkerName, Company, Entity, WorkPosition        string
	Parents, Nationality                                         string
	RequesterName, RequesterEmail, WorkLocation                  string
	McuCheckupID, McuResult, McuValidUntil, LepValidUntil        string
	Phone, EmergencyName, EmergencyPhone                         string
	Province, Regency, District, Village, Street                 string
	VisaType, VisaCode, VisaIssued, VisaIssuedBy, VisaExpiry     string
	RptkaIssued, RptkaExpiry                                     string
	RptkaDays                                                    int
	ImmigrationUntil, PoliceUntil, PassportUntil                 string
	IDDoc, IDPhoto, VisaDoc, RptkaDoc, ImmigrationDoc, PoliceDoc string
	ContractUntil, ContractDoc                                   string
	BpjsKes, BpjsKesDoc, BpjsTk, BpjsTkDoc                       string
	CertUntil, CertDoc, CertName                                 string
	PositionCanonical, PositionRisk, CertRequired                string
	DOB                                                          string // yyyy-mm-dd, for the ticket page's birth-date check
	DeclaredAt, CreatedAt                                        string
}

// AccessInfo is what the ticket page needs to know about a permit: whether the
// typed birth date matches, whether it is Approved, and what Propose reads.
type AccessInfo struct {
	RequestID, Status, DOB, WorkerName, Nationality string
	WorkLocation, McuResult, RequesterEmail         string
	LepValidUntil                                   dates.Day
}

// Access finds a permit by its LEP number (any case, spaces ignored). A blank
// number never matches, because permits waiting for review have a blank LEP.
func (s Store) Access(ctx context.Context, lep string) (AccessInfo, bool, error) {
	lep = strings.ToUpper(strings.TrimSpace(lep))
	if lep == "" {
		return AccessInfo{}, false, nil
	}
	var a AccessInfo
	var valid string
	err := s.DB.QueryRowContext(ctx, `
SELECT request_id, status, dob, worker_name, nationality, work_location, mcu_result, requester_email, lep_valid_until
FROM permit WHERE lep_no = ?`, lep).
		Scan(&a.RequestID, &a.Status, &a.DOB, &a.WorkerName, &a.Nationality, &a.WorkLocation, &a.McuResult, &a.RequesterEmail, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessInfo{}, false, nil
	}
	if err != nil {
		return AccessInfo{}, false, fmt.Errorf("read permit by LEP: %w", err)
	}
	a.LepValidUntil = dates.Day(valid)
	return a, true, nil
}

// Exists reports whether a request id is taken (the id generator retries on true).
func (s Store) Exists(ctx context.Context, id string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM permit WHERE request_id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("check request id: %w", err)
	}
	return n > 0, nil
}

// Create stores a submitted permit. The primary key is the last guard against
// two requests drawing the same id at once.
func (s Store) Create(ctx context.Context, p NewPermit) error { return s.CreateIn(ctx, s.DB, p) }

// Execer is what CreateIn needs: a *sql.DB or an open *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// CreateIn stores a permit through ex, so the importer can write inside a transaction it
// can roll back.
//
// WHY the insert is built from a column list: a 60-column INSERT with a hand-
// counted run of question marks breaks silently when a column is added.
func (s Store) CreateIn(ctx context.Context, ex Execer, p NewPermit) error {
	cols := []struct {
		name string
		val  any
	}{
		{"request_id", p.RequestID}, {"lep_no", p.LepNo}, {"status", p.Status}, {"patient_id", p.PatientID}, {"personal_id", p.PersonalID},
		{"worker_name", p.WorkerName}, {"company", p.Company}, {"entity", p.Entity}, {"work_position", p.WorkPosition},
		{"parents", p.Parents}, {"nationality", p.Nationality}, {"requester_name", p.RequesterName},
		{"requester_email", p.RequesterEmail}, {"work_location", p.WorkLocation}, {"mcu_checkup_id", p.McuCheckupID},
		{"mcu_result", p.McuResult}, {"mcu_valid_until", p.McuValidUntil}, {"lep_valid_until", p.LepValidUntil},
		{"phone", p.Phone}, {"emergency_name", p.EmergencyName}, {"emergency_phone", p.EmergencyPhone},
		{"province", p.Province}, {"regency", p.Regency}, {"district", p.District}, {"village", p.Village}, {"street", p.Street},
		{"visa_type", p.VisaType}, {"visa_code", p.VisaCode}, {"visa_issued", p.VisaIssued}, {"visa_issued_by", p.VisaIssuedBy},
		{"visa_expiry", p.VisaExpiry}, {"rptka_issued", p.RptkaIssued}, {"rptka_expiry", p.RptkaExpiry}, {"rptka_days", p.RptkaDays},
		{"immigration_until", p.ImmigrationUntil}, {"police_until", p.PoliceUntil}, {"passport_until", p.PassportUntil},
		{"id_doc", p.IDDoc}, {"id_photo", p.IDPhoto}, {"visa_doc", p.VisaDoc}, {"rptka_doc", p.RptkaDoc},
		{"immigration_doc", p.ImmigrationDoc}, {"police_doc", p.PoliceDoc},
		{"contract_until", p.ContractUntil}, {"contract_doc", p.ContractDoc},
		{"bpjs_kes", p.BpjsKes}, {"bpjs_kes_doc", p.BpjsKesDoc}, {"bpjs_tk", p.BpjsTk}, {"bpjs_tk_doc", p.BpjsTkDoc},
		{"cert_until", p.CertUntil}, {"cert_doc", p.CertDoc}, {"cert_name", p.CertName},
		{"position_canonical", p.PositionCanonical}, {"position_risk", p.PositionRisk}, {"cert_required", p.CertRequired},
		{"dob", p.DOB}, {"declared_at", p.DeclaredAt}, {"created_at", p.CreatedAt},
	}
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		names[i], marks[i], args[i] = c.name, "?", c.val
	}
	q := "INSERT INTO permit (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if _, err := ex.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("store permit: %w", err)
	}
	return nil
}
