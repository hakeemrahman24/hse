package permitlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"entry-permit/internal/dates"
)

// CardInfo is what the LEP card and its public page show. It holds no birth
// date, no e-mail, no document path and no request id: the page built from it
// is open to anyone who holds the number.
type CardInfo struct {
	LEP, Status, WorkerName, Company, Entity, Parents, Position string
	ValidUntil                                                  dates.Day
}

// Card finds a permit by its LEP number (any case) for the card page. A blank
// number never matches, because permits waiting for review have a blank LEP.
func (s Store) Card(ctx context.Context, lep string) (CardInfo, bool, error) {
	lep = strings.ToUpper(lep)
	if lep == "" {
		return CardInfo{}, false, nil
	}
	var c CardInfo
	var valid string
	err := s.DB.QueryRowContext(ctx, `
SELECT lep_no, status, worker_name, company, entity, parents, work_position, lep_valid_until
FROM permit WHERE lep_no = ?`, lep).
		Scan(&c.LEP, &c.Status, &c.WorkerName, &c.Company, &c.Entity, &c.Parents, &c.Position, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return CardInfo{}, false, nil
	}
	if err != nil {
		return CardInfo{}, false, fmt.Errorf("read card by LEP: %w", err)
	}
	c.ValidUntil = dates.Day(valid)
	return c, true, nil
}

// IDPhoto is the stored ID photo path of the permit with this LEP number, for a signed-in member of
// staff looking at the card. WHY apart from Card: CardInfo is open to anyone holding the number and
// must never carry a document path. found is false for an unknown number; the path may be blank.
func (s Store) IDPhoto(ctx context.Context, lep string) (path string, found bool, err error) {
	lep = strings.ToUpper(strings.TrimSpace(lep))
	if lep == "" {
		return "", false, nil
	}
	err = s.DB.QueryRowContext(ctx, `SELECT id_photo FROM permit WHERE lep_no = ?`, lep).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read card photo: %w", err)
	}
	return path, true, nil
}
