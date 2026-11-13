package mcusubmit

import (
	"context"
	"fmt"
	"log"
	"net/http"
)

// ReminderPage is the admin's Reminder settings: the master switch, a preview of
// who would be mailed now, and Send now. Admin only (others get the 404 of a missing path).
func (a *API) ReminderPage(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	a.reminderPage(w, r, http.StatusOK, admin, "", "")
}

func (a *API) reminderPage(w http.ResponseWriter, r *http.Request, status int, admin *Submitter, errMsg, notice string) {
	cfg, err := a.Store.ReminderSettings(r.Context())
	if err != nil {
		a.serverError(w, "reminder settings", err)
		return
	}
	subs, workers, err := a.Store.ReminderPreview(r.Context())
	if err != nil {
		a.serverError(w, "reminder preview", err)
		return
	}
	p := a.newPage("Reminders")
	p.User, p.Reminder, p.Error, p.Notice = admin, cfg, errMsg, notice
	p.Preview = fmt.Sprintf("%d accounts and %d workers would be mailed now.", subs, workers)
	a.render(w, status, "reminders.html", p)
}

// ReminderSave stores the master switch. The two day settings are no longer on the form (the mail has
// fixed stages and sends once per stage), so their stored values are kept as they are.
func (a *API) ReminderSave(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.reminderPage(w, r, http.StatusBadRequest, admin, "The form could not be read.", "")
		return
	}
	cfg, err := a.Store.ReminderSettings(r.Context())
	if err != nil {
		a.serverError(w, "reminder settings", err)
		return
	}
	cfg.Enabled = r.PostFormValue("enabled") == "on"
	if err := a.Store.SaveReminderSettings(r.Context(), cfg, admin.ID); err != nil {
		a.serverError(w, "save reminder settings", err)
		return
	}
	a.reminderPage(w, r, http.StatusOK, admin, "", "Saved.")
}

// ReminderSendNow runs the same job as the daily one, so the admin sees the real effect. It
// refuses while the master switch is off.
func (a *API) ReminderSendNow(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	cfg, err := a.Store.ReminderSettings(r.Context())
	if err != nil {
		a.serverError(w, "reminder settings", err)
		return
	}
	if !cfg.Enabled {
		a.reminderPage(w, r, http.StatusConflict, admin, "Turn reminders on and save first.", "")
		return
	}
	subs, workers, err := a.Store.RunReminders(r.Context(), a.mailer, a.baseURL)
	if err != nil {
		a.reminderPage(w, r, http.StatusBadGateway, admin, "Some mails could not be sent; they will be tried again.", "")
		return
	}
	a.reminderPage(w, r, http.StatusOK, admin, "", fmt.Sprintf("Sent to %d accounts about %d workers.", subs, workers))
}

// sampleReminderLines is what the test mail lists: made-up workers, one per stage, so the admin sees
// every Status label. They are never real people and nothing is recorded as mailed.
func (a *API) sampleReminderLines() []Expiring {
	today := a.Store.clinicToday()
	line := func(name, position string, days int) Expiring {
		return Expiring{
			Name: name, Position: position, Company: "PT Sample Company",
			ValidUntil: today.AddDate(0, 0, days).Format(dateOnlyLayout), DaysLeft: days, Stage: reminderStage(days),
		}
	}
	return []Expiring{
		line("Sample Worker One", "Welder", -10), line("Sample Worker Two", "Rigger", 9),
		line("Sample Worker Three", "Driver", 25), line("Sample Worker Four", "Operator", 52),
	}
}

// ReminderTest sends the reminder mail, filled with sample workers, to an address the admin types.
// It does not read the real lists, does not mark anyone as mailed and works while the master switch
// is off, so the wording and the look can be checked before turning reminders on.
func (a *API) ReminderTest(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.reminderPage(w, r, http.StatusBadRequest, admin, "The form could not be read.", "")
		return
	}
	to, err := ParseEmail(r.PostFormValue("to"))
	if err != nil {
		a.reminderPage(w, r, http.StatusBadRequest, admin, err.Error()+".", "")
		return
	}
	if a.mailer == nil {
		a.reminderPage(w, r, http.StatusBadGateway, admin, "No mail sender is configured.", "")
		return
	}
	lines := a.sampleReminderLines()
	ctx, cancel := context.WithTimeout(r.Context(), mailTimeout)
	defer cancel()
	if err := sendRich(ctx, a.mailer, to, "TEST: Medical checkups that expire soon", reminderBody(a.baseURL, lines), reminderHTML(a.baseURL, lines)); err != nil {
		log.Printf("mcusubmit: test reminder to %s failed: %v", to, err)
		a.reminderPage(w, r, http.StatusBadGateway, admin, "The test mail was not sent: "+err.Error(), "")
		return
	}
	a.reminderPage(w, r, http.StatusOK, admin, "", "Test reminder sent to "+to+" with sample workers. Nothing was recorded as mailed.")
}
