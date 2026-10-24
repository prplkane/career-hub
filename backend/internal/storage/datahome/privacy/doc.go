// Package privacy observes and explicitly applies the Windows private DACL
// policy for a Data Home tree. Verification and application are separate
// operations; neither takes a writer lease. Non-Windows builds return
// ACL_PLATFORM_UNSUPPORTED.
//
// VerifyPrivateTree(ctx, root) reads only the DACL of each retained handle
// using the current process token's SID. Policy: the root has a protected
// DACL with exactly two allow full-control entries, each with OI+CI
// inheritance, for the process SID and SYSTEM; descendants have unprotected
// DACLs whose entries carry ID or OI+CI+ID flags. Missing, null or empty
// DACLs, deny, object or callback entries, extra trustees and non-full
// rights fail. The root must be a local drive path, not a drive root, UNC,
// device or stream path. Each ancestor is opened and retained before the
// next component, and descendants are opened relative to their parent handle
// with reparse refusal and no delete sharing; reparse points and multiply
// linked files fail. Limits: 4,096 tree entries, 64 root components, 64
// descendant levels, 4,096 path bytes, 255 bytes per component, 1 MiB of
// SDDL per pass and 64 KiB per descriptor. A second pass rechecks identity,
// DACL and sorted membership; all handles are released on every return. The
// budget is 15 s, cooperative between OS calls. This is a point-in-time
// observation, not a lease or a defense against privileged filesystem
// manipulation, and it must not be used as an unconditional existing-home
// admission rule.
//
// ApplyPrivateTree(ctx, root) runs only after markerless bootstrap admission
// and before any private content. It retains and checks the whole bounded
// tree first, then gives the root the protected two-entry DACL and each
// descendant an empty non-null unprotected DACL so Windows rebuilds inherited
// entries; a null DACL is never passed, and owner and SACL are untouched.
// Readback verifies the exact policy. The change flag turns true before the
// first native mutation and stays true after partial failure; no rollback
// broadens old permissions. Budget 45 s, cooperative.
//
// Errors are only ACL_SETUP_FAILED, ACL_TREE_LIMIT, ACL_CONTEXT_CANCELED and
// ACL_PLATFORM_UNSUPPORTED; paths, SIDs, descriptors and OS text never
// appear.
package privacy
