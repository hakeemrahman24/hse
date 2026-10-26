package main

import (
	"context"
	"strings"
	"time"
)

// Development stubs, used only when SOC_DEV_STUBS=1. They let the whole SOC flow run before the
// Medpost API and the production database changes exist. The fixtures are invented people.

type devTurnstile struct{}

func (devTurnstile) Verify(ctx context.Context, token, ip string) (bool, error) {
	return token == "dev-pass", nil
}

type devEmployee struct {
	employee
	dob time.Time
}

type devDirectory struct{ people []devEmployee }

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func newDevDirectory() *devDirectory {
	return &devDirectory{people: []devEmployee{
		{employee{"E001", "Budi Santoso", "Operator", "SALAWATI MOTORINDO"}, d(1990, 5, 14)},
		{employee{"E002", "Siti Rahma", "Safety Officer", "STARWAY ENGINEERING INDONESIA"}, d(1988, 11, 2)},
		{employee{"E003", "Andi Wijaya", "Welder", "SALAWATI MOTORINDO"}, d(1992, 3, 9)},
		{employee{"E004", "Andi Wijaya", "Driver", "STARWAY ENGINEERING INDONESIA"}, d(1985, 7, 21)},
		{employee{"E005", "Dewi Lestari", "Supervisor", "LAYAR NUSANTARA GAS"}, d(1991, 1, 30)},
	}}
}

func (dd *devDirectory) Search(ctx context.Context, q string, max int) ([]nameMatch, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []nameMatch
	for _, p := range dd.people {
		if !strings.Contains(strings.ToLower(p.Name), q) {
			continue
		}
		dup := false
		for _, o := range dd.people {
			if o.Key != p.Key && strings.EqualFold(o.Name, p.Name) {
				dup = true
			}
		}
		out = append(out, nameMatch{Key: p.Key, Name: p.Name, Company: p.Company, Duplicate: dup})
		if len(out) == max {
			break
		}
	}
	return out, nil
}

func (dd *devDirectory) Verify(ctx context.Context, key string, dob time.Time) (*employee, error) {
	for _, p := range dd.people {
		if p.Key == key && p.dob.Equal(dob) {
			e := p.employee
			return &e, nil
		}
	}
	return nil, nil
}
