package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/worker"
)

// A worker who has proven who they are stays signed in on this device for a few hours (owner
// request 2026-11-08): the sidebar entries are pages they can reopen, and no page sends them back to
// the name box. The cookie holds only the signed proof the page already carried, never a date of birth.

// signedInWorker looks Budi up and returns the cookies the lookup set.
func (e *testEnv) signedInWorker(t *testing.T) []*http.Cookie {
	t.Helper()
	e.workers.people = []worker.Person{budi}
	v := url.Values{"name": {"Budi Santoso"}, "dob": {"05/03/1988"}, "cf-turnstile-response": {"dev-pass"}}
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/my", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noFollow().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Cookies()
}

func TestLookupSignsTheWorkerInWithAHardenedCookie(t *testing.T) {
	e := newEnv(t)
	var got *http.Cookie
	for _, c := range e.signedInWorker(t) {
		if c.Name == workerCookie {
			got = c
		}
	}
	if got == nil {
		t.Fatal("the lookup set no worker cookie")
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteLaxMode || got.MaxAge <= 0 || got.Path != "/" {
		t.Errorf("cookie not hardened: %+v", got)
	}
	if strings.Contains(got.Value, "1988") {
		t.Error("the cookie carries the date of birth")
	}
}

func TestMyPageOpensAgainWithoutSigningIn(t *testing.T) {
	e := newEnv(t)
	cookies := e.signedInWorker(t)
	resp, body := e.get(t, "/my", cookies)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"Budi Santoso", "PT Maju", "Welder", "My working tickets", `action="/my/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(body, "1988") || strings.Contains(body, `name="dob"`) {
		t.Error("the page asks for, or shows, the date of birth")
	}
}

func TestMyPageWithoutASessionGoesToSignIn(t *testing.T) {
	e := newEnv(t)
	for name, cookies := range map[string][]*http.Cookie{
		"none":    nil,
		"garbage": {{Name: workerCookie, Value: "nonsense.value"}},
	} {
		resp, _ := e.get(t, "/my", cookies)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
			t.Errorf("%s: status %d to %q, want 303 to /", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func TestSigningOutClearsTheWorkerCookie(t *testing.T) {
	e := newEnv(t)
	resp := e.postForm(t, "/my/logout", e.signedInWorker(t), url.Values{})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("status %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == workerCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the worker cookie was not cleared")
	}
}

func TestThePhotoPageLeadsBackToMyPageNotToSignIn(t *testing.T) {
	e := newEnv(t)
	_, body := e.photoPost(t, e.photoToken(t), nil)
	if !strings.Contains(body, `href="/my"`) {
		t.Error("the photo page has no way back to my page")
	}
}

// The Photos option shows the worker's current photo (owner report 2026-11-08: "why i cant see my own
// photos"), served only to them by the card photo address, and says so plainly when there is none.
func TestPhotosOptionShowsTheCurrentPhotoOrSaysThereIsNone(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	owner := e.signedInWorker(t)

	_, body := e.get(t, "/my", owner)
	if strings.Contains(body, `class="my-photo-img"`) || !strings.Contains(body, "No photo yet") {
		t.Error("with no photo the page should say so, not show a picture")
	}

	e.photoOfBudi(t)
	_, body = e.get(t, "/my", owner)
	if !strings.Contains(body, `class="my-photo-img" src="/c/`+testLEP+`/photo"`) {
		t.Error("the Photos option does not show the current photo")
	}
	if r, _ := e.get(t, "/c/"+testLEP+"/photo", owner); r.StatusCode != 200 {
		t.Errorf("the worker cannot fetch their own photo: %d", r.StatusCode)
	}
}

// "Open working schedule" on the card page must not ask a signed-in worker for their birth date
// again (owner report 2026-11-08). Anyone else is still asked.
func TestWorkingScheduleOpensWithoutAskingTheSignedInOwner(t *testing.T) {
	e := newEnv(t)
	e.cardPermit(t)
	owner := e.signedInWorker(t)
	path := "/ticket?lep=" + testLEP

	r, body := e.get(t, path, owner)
	if r.StatusCode != 200 || strings.Contains(body, `name="dob"`) || !tokenRe.MatchString(body) {
		t.Errorf("the owner was asked again, or the schedule did not open: status %d", r.StatusCode)
	}

	// The public and another worker's sign-in still get the birth-date step.
	other := []*http.Cookie{{Name: workerCookie, Value: signPhoto("test-secret", photoClaims{
		PatientID: 99, Name: "Other Person", Expires: e.app.Clock.Now().AddHours(1)})}}
	for name, cookies := range map[string][]*http.Cookie{"public": nil, "other worker": other} {
		_, body := e.get(t, path, cookies)
		if !strings.Contains(body, `name="dob"`) || tokenRe.MatchString(body) {
			t.Errorf("%s: should be asked for the birth date", name)
		}
	}
}
