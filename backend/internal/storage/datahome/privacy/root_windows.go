//go:build windows

package privacy

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

// VerifyPrivateRoot checks, on every start, that an existing Data Home root
// still has the private bootstrap policy (ADR SEC-250 item 5): a protected
// DACL granting only the current process user and SYSTEM, owned by that user
// or by the token's default owner. Windows Server and some policies make the
// Administrators group the default owner of objects an elevated administrator
// creates; administrators are outside the SEC-250 threat model anyway. Descendants inherit from this root, so the check is
// constant-time and does not walk the tree. It reads security metadata only.
func VerifyPrivateRoot(ctx context.Context, root string) (failure *Error) {
	if ctx == nil || ctx.Err() != nil {
		return fail("ACL_CONTEXT_CANCELED")
	}
	anchor, parts, ok := rootParts(root)
	if !ok {
		return fail("ACL_SETUP_FAILED")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fail("ACL_SETUP_FAILED")
	}
	principal := user.User.Sid.String()
	if !sidPattern.MatchString(principal) {
		return fail("ACL_SETUP_FAILED")
	}
	held := []*heldEntry{}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			if err := held[i].file.Close(); err != nil && failure == nil {
				failure = fail("ACL_SETUP_FAILED")
			}
		}
	}()
	// Resolve each component relative to its held parent, as VerifyPrivateTree
	// does, so a reparse point cannot redirect the check to another directory.
	var rootEntry *heldEntry
	for i := 0; i <= len(parts); i++ {
		var entry *heldEntry
		var e *Error
		if i == 0 {
			entry, e = openEntry(anchor)
		} else {
			entry, e = openChildMode(rootEntry, parts[i-1], true)
		}
		if e != nil {
			return e
		}
		held = append(held, entry)
		if !isDirectory(entry.identity) {
			return fail("ACL_SETUP_FAILED")
		}
		rootEntry = entry
	}
	budget := 0
	if _, e := readPrivateDACL(rootEntry.file, principal, false, &budget); e != nil {
		return fail("DATA_HOME_PERMISSIONS_UNSAFE")
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(rootEntry.file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || descriptor == nil {
		return fail("ACL_SETUP_FAILED")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return fail("DATA_HOME_PERMISSIONS_UNSAFE")
	}
	if owner.Equals(user.User.Sid) {
		return nil
	}
	defaultOwner, err := tokenDefaultOwner(windows.GetCurrentProcessToken())
	if err != nil || !owner.Equals(defaultOwner) {
		return fail("DATA_HOME_PERMISSIONS_UNSAFE")
	}
	return nil
}

// tokenDefaultOwner returns the SID Windows assigns as owner of objects this
// process creates (TOKEN_OWNER), copied out of the query buffer.
func tokenDefaultOwner(token windows.Token) (*windows.SID, error) {
	size := uint32(64)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		err := windows.GetTokenInformation(token, windows.TokenOwner, &buffer[0], size, &size)
		if err == nil {
			owner := (*struct{ Owner *windows.SID })(unsafe.Pointer(&buffer[0])).Owner
			if owner == nil {
				return nil, windows.ERROR_INVALID_SID
			}
			return owner.Copy()
		}
		if err != windows.ERROR_INSUFFICIENT_BUFFER {
			return nil, err
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}
