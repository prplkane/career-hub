//go:build windows

package privacy

import (
	"context"
	"runtime"
	"time"

	"golang.org/x/sys/windows"
)

// ApplyPrivateTree enforces the Windows bootstrap policy on an explicit existing
// tree, not an initialized user home chosen implicitly. The caller must quiesce
// other writers and validate markerless/bootstrap admission before this call.
//
// mayHaveChanged becomes true before the first native mutation dispatch. Errors
// after that point may leave partly restricted ACLs; retry re-applies the policy,
// never restores a previously broad ACL. No content, ownership or SACL is changed.
// OS inheritance propagation and synchronous calls are not atomically bounded by
// the preflight snapshot. Cancellation is cooperative, not native preemption.
func ApplyPrivateTree(ctx context.Context, root string) (mayHaveChanged bool, failure *Error) {
	return applyPrivateTree(ctx, root, setBootstrapDACL)
}

type daclSetter func(windows.Handle, *windows.ACL, bool) error

func setBootstrapDACL(handle windows.Handle, dacl *windows.ACL, protected bool) error {
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if protected {
		flags = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, flags, nil, nil, dacl, nil)
}

func applyPrivateTree(ctx context.Context, root string, setter daclSetter) (mayHaveChanged bool, failure *Error) {
	if ctx == nil {
		return false, fail("ACL_CONTEXT_CANCELED")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return false, fail("ACL_CONTEXT_CANCELED")
	}
	anchor, parts, ok := rootParts(root)
	if !ok {
		return false, fail("ACL_SETUP_FAILED")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return false, fail("ACL_SETUP_FAILED")
	}
	principal := user.User.Sid.String()
	if !sidPattern.MatchString(principal) {
		return false, fail("ACL_SETUP_FAILED")
	}
	// Empty is not NULL: NULL would grant everyone full access. The empty,
	// unprotected child DACL asks Windows to recompute inheritance from its parent.
	rootSD, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + principal + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return false, fail("ACL_SETUP_FAILED")
	}
	childSD, err := windows.SecurityDescriptorFromString("D:")
	if err != nil {
		return false, fail("ACL_SETUP_FAILED")
	}
	rootACL, _, rootErr := rootSD.DACL()
	childACL, _, childErr := childSD.DACL()
	if rootErr != nil || childErr != nil || rootACL == nil || childACL == nil || rootACL.AceCount != 2 || childACL.AceCount != 0 {
		return false, fail("ACL_SETUP_FAILED")
	}
	held := []*heldEntry{}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			if err := held[i].file.Close(); err != nil && failure == nil {
				failure = fail("ACL_SETUP_FAILED")
			}
		}
	}()
	var rootEntry *heldEntry
	for i := 0; i <= len(parts); i++ {
		if ctx.Err() != nil {
			return false, fail("ACL_CONTEXT_CANCELED")
		}
		var entry *heldEntry
		var e *Error
		if i == 0 {
			entry, e = openEntry(anchor)
		} else {
			access := uint32(windows.FILE_GENERIC_READ)
			if i == len(parts) {
				access |= windows.WRITE_DAC
			}
			entry, e = openChildAccess(rootEntry, parts[i-1], true, access)
		}
		if e != nil {
			return false, e
		}
		held = append(held, entry)
		if !isDirectory(entry.identity) {
			return false, fail("ACL_SETUP_FAILED")
		}
		rootEntry = entry
	}
	entries := []*heldEntry{rootEntry}
	for i := 0; i < len(entries); i++ {
		if ctx.Err() != nil {
			return false, fail("ACL_CONTEXT_CANCELED")
		}
		entry := entries[i]
		if !isDirectory(entry.identity) {
			continue
		}
		names, e := readNames(entry.file, MaxEntries-len(entries))
		if e != nil {
			return false, e
		}
		entry.names = names
		for _, name := range names {
			if ctx.Err() != nil {
				return false, fail("ACL_CONTEXT_CANCELED")
			}
			if !validName(name) || len(entry.path)+1+len(name) > MaxPathBytes || entry.depth >= MaxDepth {
				return false, fail("ACL_TREE_LIMIT")
			}
			child, e := openWritableChild(entry, name)
			if e != nil {
				return false, e
			}
			child.depth = entry.depth + 1
			held, entries = append(held, child), append(entries, child)
		}
	}
	// Refuse an observed namespace change before the first write. All descendants
	// were opened and type/link-checked before their ACLs can be modified.
	if e := checkWritableTree(ctx, held, entries); e != nil {
		return false, e
	}
	for i, entry := range entries {
		if ctx.Err() != nil {
			return mayHaveChanged, fail("ACL_CONTEXT_CANCELED")
		}
		info, e := inspectHandle(windows.Handle(entry.file.Fd()))
		if e != nil || !sameEntry(entry.identity, info) {
			return mayHaveChanged, fail("ACL_SETUP_FAILED")
		}
		dacl := childACL
		if i == 0 {
			dacl = rootACL
		}
		mayHaveChanged = true
		err := setter(windows.Handle(entry.file.Fd()), dacl, i == 0)
		runtime.KeepAlive(entry.file)
		runtime.KeepAlive(rootSD)
		runtime.KeepAlive(childSD)
		if err != nil {
			return true, fail("ACL_SETUP_FAILED")
		}
	}
	budget := 0
	for i, entry := range entries {
		if ctx.Err() != nil {
			return true, fail("ACL_CONTEXT_CANCELED")
		}
		if _, e := readPrivateDACL(entry.file, principal, i != 0, &budget); e != nil {
			return true, e
		}
	}
	if e := checkWritableTree(ctx, held, entries); e != nil {
		return true, e
	}
	return true, nil
}

// Repair needs metadata/DACL access, not permission to read opaque file content.
// Directories additionally need listing rights for the bounded preflight. The
// first handle pins identity while the directory-only wider handle is opened.
func openWritableChild(parent *heldEntry, name string) (*heldEntry, *Error) {
	entry, e := openChildAccess(parent, name, false,
		windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.WRITE_DAC|windows.SYNCHRONIZE)
	if e != nil || !isDirectory(entry.identity) {
		return entry, e
	}
	directory, e := openChildAccess(parent, name, false, windows.FILE_GENERIC_READ|windows.WRITE_DAC)
	closeErr := entry.file.Close()
	if e != nil {
		return nil, e
	}
	if closeErr != nil || !sameEntry(entry.identity, directory.identity) {
		_ = directory.file.Close()
		return nil, fail("ACL_SETUP_FAILED")
	}
	return directory, nil
}

func checkWritableTree(ctx context.Context, held, entries []*heldEntry) *Error {
	for _, entry := range held {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		info, e := inspectHandle(windows.Handle(entry.file.Fd()))
		if e != nil || !sameEntry(entry.identity, info) {
			return fail("ACL_SETUP_FAILED")
		}
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return fail("ACL_CONTEXT_CANCELED")
		}
		if isDirectory(entry.identity) {
			if e := checkNames(entry); e != nil {
				return e
			}
		}
	}
	return nil
}
