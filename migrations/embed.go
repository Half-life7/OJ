// Package migrations contains the versioned database schema shipped with this build.
package migrations

import "embed"

// Files is the SQL source for golang-migrate's iofs driver.
//
//go:embed *.sql
var Files embed.FS

const ExpectedVersion uint = 9
