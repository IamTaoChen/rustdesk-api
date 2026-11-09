package model

// PeerIdentityLock serializes device report transactions across API instances.
// Only row 1 is used; existing device rows need no destructive deduplication.
type PeerIdentityLock struct {
	Id uint `gorm:"primaryKey"`
}
