// // Package embed embeds resource files needed for container builds.
package embed

import _ "embed"

// Containerfile is the container build file for PostgreSQL 18.
//
//go:embed Containerfile
var Containerfile string

// BackupContainerfile is the container build file for the shared pgbackrest backup container.
//
//go:embed backup.Containerfile
var BackupContainerfile string

// InitShell is the PostgreSQL init script (enables WAL archiving via cp).
//
//go:embed init.sh
var InitShell string
