package service

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidPeerIdentity = errors.New("invalid device identity")
var ErrPeerIdentityConflict = errors.New("device identity conflict")

func validPeerIdentity(id, uuid string) bool {
	valid := func(s string, max int) bool {
		if s == "" || len(s) > max || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
			return false
		}
		for _, r := range s {
			if unicode.IsControl(r) {
				return false
			}
		}
		return true
	}
	// Client UUIDs are opaque strings (including base64), not necessarily RFC4122.
	return valid(id, 253) && valid(uuid, 1024)
}

// UpsertSysInfo associates a report with an existing UUID without replacing
// administrative metadata. Anonymous reports are not proof of device ownership.
func (ps *PeerService) UpsertSysInfo(info *model.Peer) (*model.Peer, error) {
	if info == nil || !validPeerIdentity(info.Id, info.Uuid) {
		return nil, ErrInvalidPeerIdentity
	}
	var peer model.Peer
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := LockPeerIdentity(tx); err != nil {
			return err
		}
		var byUUID, byID []model.Peer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("uuid = ?", info.Uuid).Limit(2).Find(&byUUID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", info.Id).Limit(2).Find(&byID).Error; err != nil {
			return err
		}
		if len(byUUID) > 1 || len(byID) > 1 {
			return ErrPeerIdentityConflict
		}
		if len(byUUID) == 1 {
			peer = byUUID[0]
			// Database collations must not make opaque UUID matching case-insensitive.
			if peer.Uuid != info.Uuid {
				return ErrPeerIdentityConflict
			}
			if len(byID) == 1 && byID[0].RowId != peer.RowId {
				return ErrPeerIdentityConflict
			}
		} else if len(byID) == 1 {
			if byID[0].Uuid != "" || byID[0].Id != info.Id {
				return ErrPeerIdentityConflict
			}
			// Legacy records without UUID can be associated only through their exact ID.
			peer = byID[0]
		}
		oldID := peer.Id
		loginID := oldID
		if loginID == "" {
			loginID = info.Id
		}
		if peer.UserId == 0 {
			userID, err := latestExactPeerUser(tx, info.Uuid, loginID)
			if err != nil {
				return err
			}
			peer.UserId = userID
		}
		if peer.RowId == 0 {
			peer.Id = info.Id
			peer.Uuid = info.Uuid
			peer.Cpu = info.Cpu
			peer.Hostname = info.Hostname
			peer.Memory = info.Memory
			peer.Os = info.Os
			peer.Username = info.Username
			peer.Version = info.Version
			return tx.Create(&peer).Error
		}
		// A map writes intentionally empty report fields but cannot clear group/alias.
		updates := map[string]interface{}{"id": info.Id, "uuid": info.Uuid, "cpu": info.Cpu,
			"hostname": info.Hostname, "memory": info.Memory, "os": info.Os,
			"username": info.Username, "version": info.Version, "user_id": peer.UserId}
		if err := tx.Model(&peer).Updates(updates).Error; err != nil {
			return err
		}
		if oldID != info.Id {
			if err := moveExactPeerTokens(tx, info.Uuid, oldID, info.Id); err != nil {
				return err
			}
		}
		return tx.First(&peer, peer.RowId).Error
	})
	if err != nil {
		return nil, err
	}
	return &peer, nil
}

// Heartbeat updates only the exact ID/UUID pair and never renames a device.
// False tells the caller to request a fresh sysinfo upload.
func (ps *PeerService) Heartbeat(id, uuid, ip string, now int64) (bool, error) {
	if !validPeerIdentity(id, uuid) {
		return false, ErrInvalidPeerIdentity
	}
	found := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockSQLiteWriter(tx); err != nil {
			return err
		}
		var peers []model.Peer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("row_id", "id", "uuid").
			Where("id = ? and uuid = ?", id, uuid).Limit(2).Find(&peers).Error; err != nil {
			return err
		}
		if len(peers) > 1 {
			return ErrPeerIdentityConflict
		}
		if len(peers) == 0 || peers[0].Id != id || peers[0].Uuid != uuid {
			return nil
		}
		// The row lock keeps exact validation and the online update together.
		if err := tx.Model(&model.Peer{}).Where("row_id = ? and last_online_time <= ?", peers[0].RowId, now-30).
			Updates(map[string]interface{}{"last_online_time": now, "last_online_ip": ip}).Error; err != nil {
			return err
		}
		found = true
		return nil
	})
	return found && err == nil, err
}

// SQL equality may be case-insensitive; continue until the latest exact pair.
func latestExactPeerUser(tx *gorm.DB, uuid, id string) (uint, error) {
	rows, err := tx.Table("login_logs AS l").Select("l.user_id, l.uuid, l.device_id, t.user_id, t.device_uuid, t.device_id").
		Joins("JOIN user_tokens AS t ON t.id = l.user_token_id").
		Where("l.uuid = ? and l.device_id = ? and t.expired_at >= ?", uuid, id, time.Now().Unix()).Order("l.id desc").Rows()
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, tokenUserID uint
		var storedUUID, storedID, tokenUUID, tokenID string
		if err := rows.Scan(&userID, &storedUUID, &storedID, &tokenUserID, &tokenUUID, &tokenID); err != nil {
			return 0, err
		}
		if storedUUID == uuid && storedID == id && tokenUUID == uuid && tokenID == id && tokenUserID == userID {
			return userID, nil
		}
	}
	return 0, rows.Err()
}

func moveExactPeerTokens(tx *gorm.DB, uuid, oldID, newID string) error {
	var tokens []model.UserToken
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "device_uuid", "device_id").
		Where("device_uuid = ? and device_id = ?", uuid, oldID).Find(&tokens).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(tokens))
	for _, token := range tokens {
		if token.DeviceUuid == uuid && token.DeviceId == oldID {
			ids = append(ids, token.Id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return tx.Model(&model.UserToken{}).Where("id IN ?", ids).Update("device_id", newID).Error
}

// LockPeerIdentity must be called inside a transaction before identity writes.
func LockPeerIdentity(tx *gorm.DB) error {
	lock := model.PeerIdentityLock{Id: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&lock).Error; err != nil {
		return err
	}
	return tx.Model(&lock).UpdateColumn("id", gorm.Expr("id")).Error
}

func exactPeerByUUID(tx *gorm.DB, uuid string) (*model.Peer, error) {
	var peers []model.Peer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("uuid = ?", uuid).Limit(2).Find(&peers).Error; err != nil {
		return nil, err
	}
	if len(peers) > 1 || (len(peers) == 1 && peers[0].Uuid != uuid) {
		return nil, ErrPeerIdentityConflict
	}
	if len(peers) == 0 {
		return nil, nil
	}
	return &peers[0], nil
}

func bindExactPeerUser(tx *gorm.DB, id, uuid string, userID uint) error {
	peer, err := exactPeerByUUID(tx, uuid)
	if err != nil {
		return err
	}
	var byID []model.Peer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Limit(2).Find(&byID).Error; err != nil {
		return err
	}
	if len(byID) > 1 || (len(byID) == 1 && byID[0].Id != id) {
		return ErrPeerIdentityConflict
	}
	if peer == nil {
		if len(byID) == 1 && byID[0].Uuid != "" {
			return ErrPeerIdentityConflict
		}
		// Login may precede the first UUID report, including legacy ID-only rows.
		return nil
	}
	if peer.Id != id || (len(byID) == 1 && byID[0].RowId != peer.RowId) {
		return ErrPeerIdentityConflict
	}
	return tx.Model(&model.Peer{}).Where("row_id = ?", peer.RowId).Update("user_id", userID).Error
}
