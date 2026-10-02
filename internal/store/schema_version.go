// SPDX-License-Identifier: MIT

package store

const schemaVersion = 14

// SchemaVersion reports the schema implemented by this binary, without I/O.
func SchemaVersion() int { return schemaVersion }
