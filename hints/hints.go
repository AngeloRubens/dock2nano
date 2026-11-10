// Package hints holds the tables that ship inside the binary.
package hints

import _ "embed"

// HubRequired is the text of hub-required.yaml: the programs for which the
// hub of ops packages holds a special build that must be used whole.
//
//go:embed hub-required.yaml
var HubRequired []byte

// HubTables is the text of hub-tables.yaml: the namespace preference, the
// variables that hold a version, and how programs are made to run as one
// process.
//
//go:embed hub-tables.yaml
var HubTables []byte
