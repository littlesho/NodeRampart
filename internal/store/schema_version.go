// SPDX-License-Identifier: MIT

package store

const schemaVersion = 13

// SchemaVersion reports the schema implemented by this binary, without I/O.
func SchemaVersion() int { return schemaVersion }
