// Package startup composes explicit Windows Data Home bootstrap with the
// read-only sidecar workspace. It does not discover profiles or select itself.
//
// Open(ctx, record) runs, with a cancellation check between each step, the
// read-only resource and boundary preflight, datahome.Bootstrap and
// readonly.Open. Core catalogs are decoded with the fixed input limits before
// any mutation, so a malformed core refuses startup before writes.
// OpenForProcess uses BootstrapForProcess, then runs schema.PrepareOwned
// after the initialized Home is admitted, rechecking identity, channel,
// recovery sentinels and resource boundary before preparation and after its
// cleanup. OpenNewJobRecoveryForProcess is a separate maintenance
// composition that claims an existing Home and admits only the new-job
// recovery sentinel.
//
// Missing candidate roots are never created to establish separation, and a
// candidate whose only existing ancestor is the drive root is refused as
// DATA_HOME_UNAVAILABLE. Preflight releases its handles before bootstrap; it
// is not a namespace reservation or writer lease, so the host keeps other
// writers quiescent. A canceled or killed startup may leave initialized
// defaults, a published marker or a partial tree; no error promises
// rollback.
//
// Error.Code forwards only allowlisted readonly, bootstrap and preparation
// codes; unexpected causes become SIDECAR_STARTUP_FAILED and observed
// cancellation returns DATA_HOME_STARTUP_CANCELLED. Messages carry no path,
// nested cause, catalog text or stage. The package opens no listener.
package startup
