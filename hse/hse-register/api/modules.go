package main

// Module is one card on the home page (same idea as hse-medpost's module picker).
type Module struct {
	Title    string
	Sub      string
	Href     string
	Settings bool // drawn in brick red to mark it as configuration, not a working module
}

// modulesFor returns the home-page cards for a role. Everyone signed in gets the three work
// modules; only admins get Settings. Any role other than admin (including the empty role of an
// old cookie) is treated as a regular user, so the safe outcome is the default.
func modulesFor(role string) []Module {
	ms := []Module{
		{Title: "Medical Activities", Sub: "Health programs and activities from the first-aid post (view only)", Href: "/activities"},
		{Title: "Work Permit Register", Sub: "Under development", Href: "/workpermitregister"},
		{Title: "HSE Observation", Sub: "Safety observations and findings", Href: "/observations"},
	}
	ms = append(ms, Module{Title: "My account", Sub: "Change your username or password", Href: "/account"})
	if role == roleAdmin {
		ms = append(ms, Module{Title: "Settings", Sub: "Users and lookup lists", Href: "/admin/settings", Settings: true})
	}
	return ms
}
