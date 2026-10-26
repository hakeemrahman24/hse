package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestMedpostCompaniesAreDistinctSortedAndTokenIsSent(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		// Rows carry fields that must be ignored (date of birth, notes).
		_, _ = w.Write([]byte(`[
			{"company":"Zeta  Corp","dateOfBirth":"1990-01-01","resultNotes":"secret"},
			{"company":"alpha co"},
			{"company":"ALPHA CO"},
			{"company":"  "},
			{"company":""},
			{"name":"no company field"}
		]`))
	}))
	defer srv.Close()

	got, err := newMedpostDirectory(srv.URL, "tok").Companies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"alpha co", "Zeta Corp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("companies = %q, want %q", got, want)
	}
	if gotAuth != "Bearer tok" || gotPath != "/api/directory/fitness" {
		t.Errorf("request = %q %q", gotAuth, gotPath)
	}
}

func TestMedpostCompaniesErrors(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }))
	defer bad.Close()
	if _, err := newMedpostDirectory(bad.URL, "x").Companies(context.Background()); err == nil {
		t.Error("401 was not an error")
	}
	notList := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"a":1}`)) }))
	defer notList.Close()
	if _, err := newMedpostDirectory(notList.URL, "x").Companies(context.Background()); err == nil {
		t.Error("a non-list answer was not an error")
	}
}

type countingSource struct {
	calls int
	list  []string
	err   error
}

func (c *countingSource) Companies(context.Context) ([]string, error) {
	c.calls++
	return c.list, c.err
}

func TestCompanyCacheCachesAndServesStaleOnError(t *testing.T) {
	src := &countingSource{list: []string{"A"}}
	c := newCompanyCache(src)
	c.ttl = time.Millisecond * 50
	ctx := context.Background()

	if got := c.List(ctx); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("first = %v", got)
	}
	c.List(ctx)
	if src.calls != 1 {
		t.Errorf("source called %d times within the cache time, want 1", src.calls)
	}

	time.Sleep(70 * time.Millisecond)
	src.err = errors.New("medpost down")
	if got := c.List(ctx); !reflect.DeepEqual(got, []string{"A"}) {
		t.Errorf("after failure got %v, want the older list", got)
	}
}

func TestCompanyCacheWithoutSourceIsEmpty(t *testing.T) {
	var c *companyCache
	if got := c.List(context.Background()); len(got) != 0 {
		t.Errorf("nil cache gave %v", got)
	}
	if got := newCompanyCache(nil).List(context.Background()); len(got) != 0 {
		t.Errorf("no source gave %v", got)
	}
}
