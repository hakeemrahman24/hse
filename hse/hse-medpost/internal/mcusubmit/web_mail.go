package mcusubmit

import "fmt"

// The mail texts are plain English for now (the portal has no Indonesian text yet; plan section 9
// lists the language question). Each link goes to a page with a Confirm button, never straight to
// a state change.

func verifyMail(baseURL, token string) string {
	return fmt.Sprintf(`Hello,

Someone asked for an account on the medical checkup submission portal with this email address.

To confirm that the address is yours, open this link and press Confirm (it works for 24 hours):

%s/mcu-submit/verify?token=%s

After you confirm, a medpost administrator reviews the request. If you did not ask for this, ignore this email and nothing will happen.
`, baseURL, token)
}

func alreadyRegisteredMail(baseURL string) string {
	return fmt.Sprintf(`Hello,

Someone tried to request an account on the medical checkup submission portal with this email address, but it already has one.

If it was you, sign in here:

%s/mcu-submit/signin

If you did not try to register, you can ignore this email.
`, baseURL)
}

func approvedMail(baseURL, token string) string {
	return fmt.Sprintf(`Hello,

Your request for an account on the medical checkup submission portal was approved.

Open this link to choose your username and password (it works for 3 days and only once):

%s/mcu-submit/setup?token=%s

If you did not ask for this account, ignore this email.
`, baseURL, token)
}

func rejectedMail(reason string) string {
	return fmt.Sprintf(`Hello,

Your request for an account on the medical checkup submission portal was not approved.

Reason: %s

If you think this is a mistake, contact the medpost administrator.
`, reason)
}
