// Package datahome validates explicit Data Home metadata and supplies the
// separate Windows bootstrap use case. Inspect never writes or changes
// permissions.
//
// Inspect(root, expectedChannel) reads the fixed format marker through the
// managed root and checks the two recovery sentinels by metadata only;
// journal contents are never read. Validation order: marker path and read,
// JSON object, application and integer version and channel, newer or older
// format, exact current fields and home ID, document-recovery sentinel,
// restore sentinel, expected channel. Format 2 is required and nothing is
// migrated; a missing marker is unavailable, never an invitation to create a
// profile. Marker bytes are bounded to 64 KiB, JSON depth to 900 and
// integers to 4300 digits; out-of-budget input reports
// DATA_HOME_INPUT_UNSUPPORTED. Errors never contain marker bytes, native
// text or absolute paths. Inspect proves neither ownership nor exclusivity,
// and multiple reads are not a snapshot.
//
// Bootstrap(ctx, root, channel) initializes an empty Windows Home; the
// caller stops other writers first. BootstrapForProcess inserts the process
// ownership claim after the root is opened and before any private read or
// write; the claim lasts until process exit. Order: validate channel and
// context and open or create the ordinary root; admit an existing marker, or
// check recovery sentinels and the allowlisted partial root; for a
// markerless root only, apply and verify the private tree DACL; ensure the
// eleven top-level directories and taxonomy parents and create only missing
// defaults with create-only publication; for a new home, verify inherited
// permissions, generate a UUIDv4 identity and publish the format-2 marker
// last; perform the owned write probe and reread admission metadata.
// Existing files, ACLs and identities of initialized homes are preserved,
// and an opaque or invalid default is never reset. No candidate facts, keys,
// database or credentials are created.
//
// BootstrapError exposes fixed Stage, Code, CauseCode and a conservative
// MayHaveChanged flag; it is not a rollback promise. A seed failure before
// the marker leaves completed defaults for retry; a failed ACL application
// is reapplied on retry, never relaxed. Both recovery sentinels win before
// any write, a directory at a default file path is refused, and create-only
// publication refuses an intervening creator. The 60-second budget is
// cooperative between synchronous OS calls.
package datahome
