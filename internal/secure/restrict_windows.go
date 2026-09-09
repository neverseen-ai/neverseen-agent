package secure

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// restrict replaces the object's DACL with one entry: full control for the user this
// process is running as.
//
// # Why a DACL and not a mode
//
// A Unix mode is ignored on NTFS. os.Chmod there only toggles the read-only
// attribute, so every 0600 in this tree is a guarantee that was stated and not kept.
//
// # Why it is marked protected
//
// PROTECTED_DACL_SECURITY_INFORMATION stops the object inheriting from its parent.
// Inside a user's profile the inherited ACL is already close to what 0600 gives, so
// this changes little; outside it — and the trace directory is configurable, so
// C:\temp is reachable — inheritance is exactly what would let any local account read
// prompts in clear. Protecting it is the whole point of doing this rather than
// trusting the location.
//
// Administrators and SYSTEM lose their inherited access here, which is deliberate and
// is the same bargain 0700 makes on Unix: root can still take ownership, and an
// administrator can still take ownership. What neither can do any more is read it
// without leaving a trace of having taken it.
func restrict(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}

	access := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		// Inherited by children, which is the point on a directory. The trace
		// directory can be pointed outside the profile, and a file written into it by
		// anything other than secure.WriteFile would otherwise pick up whatever the
		// default was. Inheriting an owner-only grant is the safe outcome here, not a
		// leak: what must not be inherited is the *parent's* access list, and
		// PROTECTED_DACL below is what stops that.
		Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user),
		},
	}}

	acl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return fmt.Errorf("build the access list for %s: %w", path, err)
	}

	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

// currentUserSID reports who this process is running as.
//
// From the process token rather than from a username: a name has to be looked up
// against a domain that may not answer, and a logon task starting at boot is exactly
// when it would not.
func currentUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read this process's user: %w", err)
	}
	return user.User.Sid, nil
}

// isRestricted checks the property restrict sets and inheritance does not: the DACL is
// protected, so where the path sits no longer decides who can read it.
//
// Not a mode. Every test that used to assert 0600 or 0700 would fail here on a
// filesystem that has no such thing, which is the failure the Windows CI job was added
// to surface.
func isRestricted(path string) (bool, string) {
	sd, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Sprintf("cannot read the security descriptor of %s: %v", path, err)
	}

	control, _, err := sd.Control()
	if err != nil {
		return false, fmt.Sprintf("cannot read the control flags of %s: %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, fmt.Sprintf("%s inherits its access list, so where it sits decides who can read it", path)
	}

	acl, _, err := sd.DACL()
	if err != nil {
		return false, fmt.Sprintf("cannot read the access list of %s: %v", path, err)
	}
	if acl == nil {
		return false, fmt.Sprintf("%s has no access list at all", path)
	}
	// The entries themselves are deliberately not enumerated. golang.org/x/sys at the
	// version this module pins exports neither GetAce nor ACCESS_ALLOWED_ACE, and
	// moving a dependency to sharpen a test assertion is the wrong trade — the
	// protected flag above is the property that actually differs between a path this
	// package restricted and one left to inherit, which is what these tests are for.
	return true, ""
}
