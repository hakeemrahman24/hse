package mcusubmit

import (
	"fmt"
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
