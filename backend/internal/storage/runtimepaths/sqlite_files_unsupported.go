//go:build !windows

package runtimepaths

const sqliteFilesSupported = false

func guardSQLiteFiles(*Root, string) (*SQLiteFiles, *Error) {
	return nil, failure("SQLITE_FILES_UNSUPPORTED")
}
func (*SQLiteFiles) check() *Error    { return failure("SQLITE_FILES_UNSUPPORTED") }
func (*SQLiteFiles) syncMain() *Error { return failure("SQLITE_FILES_UNSUPPORTED") }
