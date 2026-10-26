package main

import "testing"

var (
	arif = viewer{Username: "arif", Role: roleAdmin}
	budi = viewer{Username: "budi", Role: roleUser}
	siti = viewer{Username: "siti", Role: roleUser}
)

func staffPost(status string) *Observation {
	return &Observation{ID: 1, Source: "Internal Audit", CreatedBy: "budi", Status: status, Category: catUnsafeAction}
}

func TestCreatorEditsOwnPostUntilClosed(t *testing.T) {
	for _, status := range []string{statusOpen, statusInProgress} {
		p := permsFor(staffPost(status), budi)
		if !p.IsOwner || !p.CanEdit || !p.CanEditEvidence {
			t.Errorf("%s: the creator must be able to edit: %+v", status, p)
		}
	}
	if p := permsFor(staffPost(statusClosed), budi); p.CanEdit || p.CanEditEvidence || p.CanAddAfter || p.CanClose || p.CanStart {
		t.Errorf("a closed post is locked even for its creator: %+v", p)
	}
}

func TestOtherUsersCanOnlyAddAfterEvidenceAndProgress(t *testing.T) {
	p := permsFor(staffPost(statusOpen), siti)
	if p.CanEdit || p.CanEditEvidence || p.IsOwner || p.CanVerify || p.CanDelete {
		t.Errorf("another user must not edit, verify or delete: %+v", p)
	}
	if !p.CanAddAfter || !p.CanStart || !p.CanClose {
		t.Errorf("another user can add After files and start/close: %+v", p)
	}
	// Even an administrator is "another user" for a post someone else created.
	if p := permsFor(staffPost(statusOpen), arif); p.CanEdit {
		t.Errorf("an administrator cannot edit someone else's post: %+v", p)
	}
}

func TestStartOnlyFromOpen(t *testing.T) {
	if permsFor(staffPost(statusInProgress), siti).CanStart {
		t.Error("an In Progress post cannot be started again")
	}
}

func TestOnlyAdminsDelete(t *testing.T) {
	if !permsFor(staffPost(statusClosed), arif).CanDelete {
		t.Error("an administrator can delete, even a closed post")
	}
	if permsFor(staffPost(statusOpen), budi).CanDelete {
		t.Error("a regular user cannot delete, not even their own post")
	}
}

func TestSOCPostsAreNotEditedByStaffButCanBeVerified(t *testing.T) {
	post := &Observation{ID: 2, Source: sourceSOC, EmployeeID: "E001", Status: statusOpen, Verification: verifNotYet}
	for _, v := range []viewer{arif, budi} {
		p := permsFor(post, v)
		if p.CanEdit || p.CanEditEvidence {
			t.Errorf("%s: staff must not edit what an employee sent: %+v", v.Username, p)
		}
		if !p.CanVerify || !p.CanAddAfter {
			t.Errorf("%s: staff can verify and add After files: %+v", v.Username, p)
		}
	}
	post.Verification = verifVerified
	if permsFor(post, siti).CanVerify {
		t.Error("an already verified post cannot be verified again")
	}
	// Closed by the submitter, not yet verified: HSE can still verify it, nothing else changes.
	post.Status, post.Verification = statusClosed, verifNotYet
	p := permsFor(post, siti)
	if !p.CanVerify {
		t.Error("a post the employee closed still waits for HSE verification")
	}
	if !p.Locked || p.CanEdit || p.CanAddAfter || p.CanClose || p.CanStart {
		t.Errorf("a closed post stays locked even while waiting for verification: %+v", p)
	}
	post.Verification = verifVerified
	if permsFor(post, siti).CanVerify {
		t.Error("closed and verified is final")
	}
}

func TestEmployeeCanAddAfterAndCloseUntilClosed(t *testing.T) {
	for _, tc := range []struct {
		status, verification string
		edit, after          bool
	}{
		{statusOpen, verifNotYet, true, true},
		{statusInProgress, verifNotYet, true, true},
		{statusOpen, verifVerified, false, true}, // verified: fields locked, After and close still open
		{statusClosed, verifNotYet, false, false},
		{statusClosed, verifVerified, false, false},
	} {
		if socOwnerCanEdit(tc.status, tc.verification) != tc.edit || socOwnerCanAddAfter(tc.status) != tc.after {
			t.Errorf("%s/%s: edit=%v after=%v", tc.status, tc.verification, socOwnerCanEdit(tc.status, tc.verification), socOwnerCanAddAfter(tc.status))
		}
	}
}

func TestStaffPostsHaveNoVerification(t *testing.T) {
	if permsFor(staffPost(statusOpen), siti).CanVerify {
		t.Error("only SOC posts can be verified")
	}
}

func TestLegacyRowsAreEditedByAdminsOnly(t *testing.T) {
	old := &Observation{ID: 3, Status: statusOpen} // no source, no creator
	if !permsFor(old, arif).CanEdit {
		t.Error("an administrator can fix an old row")
	}
	if permsFor(old, budi).CanEdit {
		t.Error("a regular user cannot edit an old row")
	}
	old.Status = statusClosed
	if permsFor(old, arif).CanEdit {
		t.Error("a closed old row is locked too")
	}
}

func TestSOCOwnerEditWindow(t *testing.T) {
	cases := []struct {
		status, verification string
		want                 bool
	}{
		{statusOpen, verifNotYet, true},
		{statusInProgress, verifNotYet, true},
		{statusOpen, verifVerified, false},
		{statusClosed, verifNotYet, false},
		{statusClosed, verifVerified, false},
	}
	for _, c := range cases {
		if got := socOwnerCanEdit(c.status, c.verification); got != c.want {
			t.Errorf("socOwnerCanEdit(%s,%s) = %v, want %v", c.status, c.verification, got, c.want)
		}
	}
}
