package main

// Who may do what with an observation. One function decides, so the pages (which buttons to show)
// and the handlers (what to allow) cannot disagree.
//
//   - A Closed post is locked: nobody can edit it, add or remove files, or change its status.
//     Only an administrator can delete it.
//   - The creator can edit their own input while the post is not closed. A staff user is the
//     creator of the posts they entered. A post sent through the public SOC form belongs to the
//     employee who sent it, so no staff user edits it; the employee edits it from "My submissions"
//     until HSE verifies it (see socOwnerCanEdit).
//   - After evidence and closing: every staff user can add "after" evidence (photos, PDFs) and move
//     the post to In Progress or Closed. The employee who sent a SOC post can add After photos and
//     close it too, from "My submissions" (socOwnerCanAddAfter), even once it is verified.
//   - Verification waits for HSE: any staff user can verify a SOC post, also after the submitter has
//     closed it. Verifying changes nothing else, so a closed post stays locked. (The creator of a
//     SOC post is never staff.)
//   - Old rows that have no recorded creator can be edited by an administrator until they are closed.

type viewer struct {
	Username string
	Role     string
}

func (v viewer) isAdmin() bool { return v.Role == roleAdmin }

type obsPerms struct {
	Locked          bool // closed
	IsOwner         bool
	CanEdit         bool // change the fields
	CanEditEvidence bool // add or remove "before" files (part of the creator's input)
	CanAddAfter     bool // add or remove "after" files
	CanStart        bool // Open -> In Progress
	CanClose        bool // -> Closed (still needs After files, unless Positive Finding)
	CanVerify       bool
	CanDelete       bool
}

func permsFor(o *Observation, v viewer) obsPerms {
	p := obsPerms{Locked: o.Status == statusClosed, CanDelete: v.isAdmin()}
	// Verification is HSE's confirmation of the report. It is open until given, closed post or not.
	p.CanVerify = o.Source == sourceSOC && o.Verification != verifVerified
	p.IsOwner = o.Source != sourceSOC && o.CreatedBy != "" && o.CreatedBy == v.Username
	legacyAdmin := o.Source != sourceSOC && o.CreatedBy == "" && v.isAdmin()
	if p.Locked {
		return p
	}
	p.CanEdit = (p.IsOwner || legacyAdmin) && o.Verification != verifVerified
	p.CanEditEvidence = p.CanEdit
	p.CanAddAfter = true
	p.CanStart = o.Status == statusOpen
	p.CanClose = true
	return p
}

// socOwnerCanAddAfter: the employee can add After photos and close their post until it is closed,
// whether or not HSE has verified it yet.
func socOwnerCanAddAfter(status string) bool { return status != statusClosed }

// socOwnerCanEdit is the same rule for the employee who sent a SOC post: until it is closed or
// verified.
func socOwnerCanEdit(status, verification string) bool {
	return status != statusClosed && verification != verifVerified
}
