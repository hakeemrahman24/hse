package dashboard

import (
	"context"
	"strings"
)

// RingRule is one row of the "ring" list: a place name at a region level and the ring it belongs to.
type RingRule struct{ Ring, Label, Level string }

// Place is the address written on a permit request.
type Place struct{ Nationality, Province, Regency, District, Village string }

const (
	ringForeign = "Foreign"
	ringPapua   = "Ring4"
	ringOther   = "Indonesian"
	// NoRing labels a permit with no place on record; WaiverRing labels every visitor.
	NoRing, WaiverRing = "(No ring)", "Waiver"
)

// levelRank orders the rule levels most specific first: village, district, regency.
func levelRank(level string) int {
	l := strings.ToLower(level)
	switch {
	case strings.Contains(l, "desa") || strings.Contains(l, "village"):
		return 0
	case strings.Contains(l, "distrik") || strings.Contains(l, "district") || strings.Contains(l, "kecamatan"):
		return 1
	case strings.Contains(l, "kabupaten") || strings.Contains(l, "regency"):
		return 2
	}
	return -1
}

// plain lowercases a place name and drops the "kabupaten " / "distrik " prefix, as the legacy system did.
func plain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "kabupaten ")
	return strings.TrimPrefix(s, "distrik ")
}

// ComputeRing gives a permit its ring from the address on the request (legacy rule): a foreigner is
// "Foreign"; otherwise the most specific matching rule wins (village, then district, then regency);
// the Papua provinces without a rule are Ring4; everyone else is "Indonesian". No address at all is "".
func ComputeRing(p Place, rules []RingRule) string {
	if !strings.EqualFold(strings.TrimSpace(p.Nationality), "indonesia") {
		return ringForeign
	}
	if p.Province == "" && p.Regency == "" && p.District == "" && p.Village == "" {
		return ""
	}
	places := []string{p.Village, p.District, p.Regency}
	for rank, place := range places {
		want := plain(place)
		if want == "" {
			continue
		}
		for _, r := range rules {
			if levelRank(r.Level) != rank {
				continue
			}
			if got := plain(r.Label); got != "" && (got == want || strings.Contains(want, got) || strings.Contains(got, want)) {
				return r.Ring
			}
		}
	}
	if strings.Contains(strings.ToLower(p.Province), "papua") {
		return ringPapua
	}
	return ringOther
}

// PermitPlaces reads the address and nationality of every permit with an LEP, by LEP.
func (s Store) PermitPlaces(ctx context.Context) (map[string]Place, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT lep_no, nationality, province, regency, district, village FROM permit WHERE lep_no <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Place{}
	for rows.Next() {
		var lep string
		var p Place
		if err := rows.Scan(&lep, &p.Nationality, &p.Province, &p.Regency, &p.District, &p.Village); err != nil {
			return nil, err
		}
		out[lep] = p
	}
	return out, rows.Err()
}

// WithRings returns the rows with Ring filled in: visitors read "Waiver", permits their computed ring.
func WithRings(rows []Row, places map[string]Place, rules []RingRule) []Row {
	out := make([]Row, len(rows))
	for i, r := range rows {
		switch {
		case r.Kind == KindVisitor:
			r.Ring = WaiverRing
		default:
			if r.Ring = ComputeRing(places[r.ID], rules); r.Ring == "" {
				r.Ring = NoRing
			}
		}
		out[i] = r
	}
	return out
}

// RingDistribution counts rows per ring, sorted by ring name.
func RingDistribution(rows []Row) []Slice {
	return distribute(rows, func(r Row) string { return r.Ring })
}
