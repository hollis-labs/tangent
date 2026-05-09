package db

import "embed"

const migrationsDir = "migrations"

//go:embed all:migrations
var migrationsFS embed.FS
