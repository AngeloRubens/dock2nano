// Package hints holds the tables that ship inside the binary.
package hints

import _ "embed"

// HubRequired is the text of hub-required.yaml: the programs for which the
// hub of ops packages holds a special build that must be used whole.
//
//go:embed hub-required.yaml
var HubRequired []byte
