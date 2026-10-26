package entrymcu

import (
	"errors"
	"strings"

	"hse-medpost/internal/staff"
)

// signCertificatePDF seals the PDF with the checkup reviewer's stored .p12.
// The reviewer name is the staff display name saved when the checkup was entered.
func (a *API) signCertificatePDF(cfg CertConfig, reviewer string, pdf []byte) ([]byte, error) {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" || strings.TrimSpace(cfg.SigningDir) == "" {
		return nil, staff.ErrNoSigningCertificate
	}
	rows, err := a.DB.Query(`
		SELECT s.StaffID
		FROM MedpostStaffTable s
		WHERE s.StaffName = ?
		  AND NOT EXISTS (
			SELECT 1 FROM AppUserTable u
			WHERE u.FK_StaffID = s.StaffID AND LOWER(u.Username) = 'admin'
		  )`, reviewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, staff.ErrNoSigningCertificate
	}
	var missing error
	for _, id := range ids {
		signed, err := staff.SignPDF(cfg.SigningDir, id, reviewer, pdf)
		if err == nil {
			return signed, nil
		}
		if errors.Is(err, staff.ErrNoSigningCertificate) {
			missing = err
			continue
		}
		return nil, err
	}
	if missing != nil {
		return nil, missing
	}
	return nil, staff.ErrNoSigningCertificate
}
