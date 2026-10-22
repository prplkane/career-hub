//go:build !windows

package runtimepaths

func reserveDirectory(*Root, string, directoryReservationOperations) (*DirectoryReservation, DirectoryCreationOutcome, *Error) {
	return nil, DirectoryNotCreated, failure("DIRECTORY_RESERVATION_UNSUPPORTED")
}
