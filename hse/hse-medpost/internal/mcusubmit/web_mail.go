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

// alreadyHasAccountMail goes to an address the admin tried to add as a new account while it already
// has one: the person learns the account exists and where to sign in or reset the password.
func alreadyHasAccountMail(baseURL string) string {
	return fmt.Sprintf(`Hello,

The medpost administrator tried to create an account for this email address on the medical checkup submission portal, but it already has one.

Sign in here:

%s/mcu-submit/signin

If you forgot your password, choose "Forgot password" on that page to get a link for a new one.
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

func resetMail(baseURL, token string) string {
	return fmt.Sprintf(`Hello,

Someone asked to reset the password of your account on the medical checkup submission portal.

Open this link to choose a new password (it works for 1 hour and only once):

%s/mcu-submit/reset?token=%s

If you did not ask for this, ignore this email: your password stays as it is.
`, baseURL, token)
}

func emailChangeMail(baseURL, token string) string {
	return fmt.Sprintf(`Hello,

A request was made to use this address as the login email of an account on the medical checkup submission portal.

To confirm, open this link and press Confirm (it works for 24 hours and only once):

%s/mcu-submit/email-change?token=%s

If you did not ask for this, ignore this email: nothing changes.
`, baseURL, token)
}

func emailChangeNoticeMail(baseURL string) string {
	return fmt.Sprintf(`Hello,

A request was made to change the login email of your account on the medical checkup submission portal to another address. Nothing changes until that address confirms.

If this was you, you can ignore this message. If it was not you, someone may have your password: reset it now at

%s/mcu-submit/forgot
`, baseURL)
}
