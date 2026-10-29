package mcusubmit

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// ReminderPage is the admin's Reminder settings: the master switch, the two windows, a preview of
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
	p.Preview = fmt.Sprintf("%d submitters and %d workers would be mailed now.", subs, workers)
	a.render(w, status, "reminders.html", p)
}

// ReminderSave stores the switch and the two windows.
func (a *API) ReminderSave(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if !parseForm(w, r) {
		a.reminderPage(w, r, http.StatusBadRequest, admin, "The form could not be read.", "")
		return
	}
	days, _ := strconv.Atoi(r.PostFormValue("days_before")) // a non-number becomes 0, which the store refuses
	repeat, _ := strconv.Atoi(r.PostFormValue("repeat_days"))
	cfg := ReminderConfig{Enabled: r.PostFormValue("enabled") == "on", DaysBefore: days, RepeatDays: repeat}
	if err := a.Store.SaveReminderSettings(r.Context(), cfg, admin.ID); err != nil {
		if errors.Is(err, ErrBadReminderSetting) {
			a.reminderPage(w, r, http.StatusBadRequest, admin, "Enter whole days from 1 to 365.", "")
			return
		}
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
	a.reminderPage(w, r, http.StatusOK, admin, "", fmt.Sprintf("Sent to %d submitters about %d workers.", subs, workers))
}
