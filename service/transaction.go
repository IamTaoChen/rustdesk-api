package service

import "gorm.io/gorm"

// SQLite has one writer for the whole database and ignores FOR UPDATE. Reuse
// the existing coordination row to acquire its write lock before any reads.
func lockSQLiteWriter(tx *gorm.DB) error {
	if tx.Dialector.Name() == "sqlite" {
		return LockPeerIdentity(tx)
	}
	return nil
}
