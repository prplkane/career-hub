// Package secretstore defines the explicitly selected credential-store
// boundary. It provides no listing, plaintext fallback or automatic key
// regeneration and never logs.
//
// Facade. New(channel, backend) wraps an explicit Backend; nil and typed-nil
// backends fail. Targets are CareerHub/<channel>/provider/<name>, where name
// is 1 to 80 ASCII characters starting with a letter or digit and otherwise
// letters, digits, dot, underscore or hyphen, with no trimming or folding.
// Set validates the name, then requires a nonempty valid-UTF-8 value of at
// most 512 bytes. Get distinguishes absent from stored-empty, validates
// UTF-8 and applies no read limit of its own. Delete returns the backend's
// exact found/deleted result. ValidationError never prints an input;
// BackendOperationError records the attempted operation, formats safely and
// unwraps the cause for sentinel checks, which must not be logged.
//
// Windows backend. NewWindowsBackend binds advapi32.dll through a lazy
// system DLL and resolves all four functions before returning; a resolution
// failure is a fixed unavailable error. Writes use CredWriteW with the
// generic type, local-machine persistence and username CareerHub; reads and
// deletes use the generic type. Only Win32 error 1168 means missing;
// access-denied and other errors are failures. Blobs are bounded to 2,560
// bytes for write and read, preserve embedded NULs and empty values, and are
// copied before CredFree. Targets must be scalar UTF-8 without NUL and at
// most 32,767 UTF-16 units, passed unchanged; Windows resolves them
// case-insensitively. BackendError carries only Code, Operation and the
// numeric Win32 code: SECRET_STORE_UNAVAILABLE, SECRET_STORE_WINDOWS_ERROR,
// SECRET_TARGET_INVALID, SECRET_BLOB_TOO_LARGE or
// SECRET_BACKEND_INVALID_RESULT. The non-Windows constructor refuses rather
// than selecting another store.
package secretstore
