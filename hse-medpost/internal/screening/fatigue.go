package screening

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
)

const formFatigue = "FatigueScoring"

// flexText keeps a JSON string, number, or boolean as text, and a null as an
// empty string. The fatigue form sends scores as numbers and blank values as
// null, so a plain string field would reject a whole record.
type flexText string

func (f *flexText) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*f = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexText(v)
	case strings.HasPrefix(s, "{") || strings.HasPrefix(s, "["):
		return errInvalidRecord
	default:
		*f = flexText(s)
	}
	return nil
}

// fatigueRecord holds the key columns of one fatigue score sheet. Keys match
// the form's toRow(). The raw sleep and wake hours (Sleep24h = X, Sleep48h = Y,
// AwakeHours = Z) have their own columns; the Score fields are the points the
// form computed from them. The medicine answers stay in RawJSON only, the same
// as the drug & alcohol record.
type fatigueRecord struct {
	ID          flexText `json:"id"`
	CreatedAt   flexText `json:"created_at"`
	Examiner    flexText `json:"examiner"`
	Name        flexText `json:"name"`
	Company     flexText `json:"company"`
	Position    flexText `json:"position"`
	Date        flexText `json:"date"`
	Time        flexText `json:"time"`
	BpSystolic  flexText `json:"bp_systolic"`
	BpDiastolic flexText `json:"bp_diastolic"`
	Pulse       flexText `json:"pulse"`
	Sleep24h    flexText `json:"sleep_24h"`
	Sleep48h    flexText `json:"sleep_48h"`
	AwakeHours  flexText `json:"awake_hours"`
	Score24h    flexText `json:"score_24h"`
	Score48h    flexText `json:"score_48h"`
	ScoreAwake  flexText `json:"score_awake"`
	ScoreTotal  flexText `json:"score_total"`
	RiskLevel   flexText `json:"risk_level"`
	RiskPoints  flexText `json:"risk_points"`
	Remarks     flexText `json:"remarks"`
}

// parseFatigueRecord applies the same limits as parseRecord: a required id of
// at most maxIDLength characters, and no field longer than maxFieldLength.
func parseFatigueRecord(raw json.RawMessage) (fatigueRecord, error) {
	var rec fatigueRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return fatigueRecord{}, errInvalidRecord
	}
	if rec.ID == "" || utf8.RuneCountInString(string(rec.ID)) > maxIDLength {
		return fatigueRecord{}, errInvalidRecord
	}
	for _, v := range []flexText{rec.CreatedAt, rec.Examiner, rec.Name, rec.Company, rec.Position,
		rec.Date, rec.Time, rec.BpSystolic, rec.BpDiastolic, rec.Pulse, rec.Sleep24h, rec.Sleep48h, rec.AwakeHours, rec.Score24h, rec.Score48h,
		rec.ScoreAwake, rec.ScoreTotal, rec.RiskLevel, rec.RiskPoints, rec.Remarks} {
		if utf8.RuneCountInString(string(v)) > maxFieldLength {
			return fatigueRecord{}, errInvalidRecord
		}
	}
	// Same rule as parseRecord: store the shared 19-character UTC layout.
	createdAt, err := dates.NormalizeTimestamp(string(rec.CreatedAt))
	if err != nil {
		return fatigueRecord{}, errInvalidRecord
	}
	rec.CreatedAt = flexText(createdAt)
	return rec, nil
}

// insertFatigue stores one fatigue record unless its ClientID is already
// present, the same way insert does for drug & alcohol results.
func (a *API) insertFatigue(staffID int64, formVersion string, rec fatigueRecord, raw json.RawMessage) error {
	var existing int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM FatigueScreening WHERE ClientID = ?`, string(rec.ID)).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	_, err := a.DB.Exec(`
		INSERT INTO FatigueScreening
			(ClientID, FK_StaffID, FormVersion, CreatedAt, SubmittedAt,
			 Examiner, WorkerName, WorkerCompany, WorkerPosition, ExamDate, ExamTime,
			 BpSystolic, BpDiastolic, Pulse, Sleep24h, Sleep48h, AwakeHours,
			 Score24h, Score48h, ScoreAwake, ScoreTotal,
			 RiskLevel, RiskPoints, Remarks, RawJSON)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(rec.ID), staffID, formVersion, string(rec.CreatedAt), dates.Stamp(a.now()),
		string(rec.Examiner), string(rec.Name), string(rec.Company), string(rec.Position),
		string(rec.Date), string(rec.Time), string(rec.BpSystolic), string(rec.BpDiastolic),
		string(rec.Pulse), string(rec.Sleep24h), string(rec.Sleep48h), string(rec.AwakeHours), string(rec.Score24h), string(rec.Score48h), string(rec.ScoreAwake),
		string(rec.ScoreTotal), string(rec.RiskLevel), string(rec.RiskPoints), string(rec.Remarks),
		string(raw),
	)
	return err
}

// SubmitFatigue: POST /api/screening/fatigue. Same contract as
// SubmitDrugAlcohol: the response lists the ids now stored, and the form
// keeps any record that is not listed.
func (a *API) SubmitFatigue(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	sess, ok := a.authorize(w, r)
	if !ok {
		return
	}
	body, ok := decodeBatch(w, r, formFatigue)
	if !ok {
		return
	}

	accepted := []string{}
	for _, raw := range body.Records {
		rec, err := parseFatigueRecord(raw)
		if err != nil {
			continue
		}
		if err := a.insertFatigue(sess.StaffID, body.FormVersion, rec, raw); err != nil {
			log.Printf("screening: fatigue insert failed (client id %s, staff %d): %v", rec.ID, sess.StaffID, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		accepted = append(accepted, string(rec.ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted})
}
