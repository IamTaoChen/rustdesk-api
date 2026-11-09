package service

import (
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func managedIdentityValid(peer *model.Peer) bool {
	if peer == nil {
		return false
	}
	uuid := peer.Uuid
	if uuid == "" {
		uuid = "_"
	}
	return validPeerIdentity(peer.Id, uuid)
}

func managedIdentityAvailable(tx *gorm.DB, peer *model.Peer, except uint) error {
	var matches []model.Peer
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", peer.Id)
	if peer.Uuid != "" {
		query = query.Or("uuid = ?", peer.Uuid)
	}
	if err := query.Find(&matches).Error; err != nil {
		return err
	}
	for _, match := range matches {
		if match.RowId != except {
			return ErrPeerIdentityConflict
		}
	}
	return nil
}

func createManagedPeer(peer *model.Peer) error {
	if !managedIdentityValid(peer) {
		return ErrInvalidPeerIdentity
	}
	copy := *peer
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := LockPeerIdentity(tx); err != nil {
			return err
		}
		if err := managedIdentityAvailable(tx, &copy, 0); err != nil {
			return err
		}
		return tx.Create(&copy).Error
	})
	if err == nil {
		*peer = copy
	}
	return err
}

func updateManagedPeer(peer *model.Peer) error {
	if !managedIdentityValid(peer) || peer.RowId == 0 {
		return ErrInvalidPeerIdentity
	}
	copy := *peer
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := LockPeerIdentity(tx); err != nil {
			return err
		}
		var previous model.Peer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&previous, copy.RowId).Error; err != nil {
			return err
		}
		if previous.Uuid != "" {
			if copy.Uuid == "" {
				copy.Uuid = previous.Uuid
			}
			if copy.Uuid != previous.Uuid {
				return ErrPeerIdentityConflict
			}
		}
		if err := managedIdentityAvailable(tx, &copy, copy.RowId); err != nil {
			return err
		}
		if err := tx.Model(&model.Peer{}).Where("row_id = ?", copy.RowId).
			Select("id", "cpu", "hostname", "memory", "os", "username", "uuid", "version", "group_id", "alias").Updates(&copy).Error; err != nil {
			return err
		}
		if previous.Id != copy.Id && copy.Uuid != "" {
			return moveExactPeerTokens(tx, copy.Uuid, previous.Id, copy.Id)
		}
		return nil
	})
	if err == nil {
		*peer = copy
	}
	return err
}

func clearExactPeerUser(tx *gorm.DB, uuid string, userID uint) error {
	if uuid == "" {
		return nil
	}
	var peers []model.Peer
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("row_id", "uuid", "user_id").Where("uuid = ? and user_id = ?", uuid, userID).Find(&peers).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(peers))
	for _, peer := range peers {
		if peer.Uuid == uuid && peer.UserId == userID {
			ids = append(ids, peer.RowId)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return tx.Model(&model.Peer{}).Where("row_id IN ?", ids).Update("user_id", 0).Error
}

func deleteExactPeerTokens(tx *gorm.DB, uuid string) error {
	if uuid == "" {
		return nil
	} // Empty UUID belongs to browser sessions too.
	var tokens []model.UserToken
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "device_uuid").Where("device_uuid = ?", uuid).Find(&tokens).Error; err != nil {
		return err
	}
	ids := make([]uint, 0, len(tokens))
	for _, token := range tokens {
		if token.DeviceUuid == uuid {
			ids = append(ids, token.Id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return tx.Where("id IN ?", ids).Delete(&model.UserToken{}).Error
}

func deleteManagedPeers(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := LockPeerIdentity(tx); err != nil {
			return err
		}
		var peers []model.Peer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("row_id IN ?", ids).Find(&peers).Error; err != nil {
			return err
		}
		for _, peer := range peers {
			if err := deleteExactPeerTokens(tx, peer.Uuid); err != nil {
				return err
			}
		}
		return tx.Where("row_id IN ?", ids).Delete(&model.Peer{}).Error
	})
}
