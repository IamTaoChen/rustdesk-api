package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/lejianwen/rustdesk-api/v2/global"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"github.com/lejianwen/rustdesk-api/v2/utils"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"gorm.io/gorm"
	"time"
)

func migrateDatabase(db *gorm.DB, version uint, bootstrap func(*gorm.DB) error) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("database unavailable: %w", err)
	}
	var previous uint
	if db.Migrator().HasTable(&model.Version{}) {
		var latest model.Version
		err := db.Order("version desc").First(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		previous = latest.Version
		if previous > version {
			return errors.New("database schema is newer than this binary")
		}
	}
	// MySQL DDL may auto-commit. Keep the marker untouched until all schema work succeeds.
	if err := db.AutoMigrate(
		&model.Version{},
		&model.User{},
		&model.UserToken{},
		&model.Tag{},
		&model.AddressBook{},
		&model.Peer{},
		&model.PeerIdentityLock{},
		&model.Group{},
		&model.UserThird{},
		&model.Oauth{},
		&model.LoginLog{},
		&model.ShareRecord{},
		&model.AuditConn{},
		&model.AuditFile{},
		&model.AddressBookCollection{},
		&model.AddressBookCollectionRule{},
		&model.ServerCmd{},
		&model.DeviceGroup{},
	); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := service.LockPeerIdentity(tx); err != nil {
			return err
		}
		var latest model.Version
		err := tx.Order("version desc").First(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if latest.Version > version {
			return errors.New("database schema is newer than this binary")
		}
		if latest.Version == version {
			return nil
		}
		if latest.Version > 0 && latest.Version < 245 {
			for _, query := range []string{
				"update oauths set oauth_type = op",
				"update oauths set issuer = 'https://accounts.google.com' where op = 'google'",
				"update user_thirds set oauth_type = third_type, op = third_type",
			} {
				if err := tx.Exec(query).Error; err != nil {
					return err
				}
			}
			var thirds []model.UserThird
			if err := tx.Where("oauth_type = ?", "google").Find(&thirds).Error; err != nil {
				return err
			}
			for _, third := range thirds {
				if third.UserId > 0 {
					if err := tx.Model(&model.User{}).Where("id = ?", third.UserId).Update("email", third.OpenId).Error; err != nil {
						return err
					}
				}
			}
		}
		if latest.Version > 0 && latest.Version < 246 {
			if err := tx.Exec("update oauths set issuer = 'https://accounts.google.com' where op = 'google' and issuer is null").Error; err != nil {
				return err
			}
		}
		if latest.Version == 0 && bootstrap != nil {
			var users int64
			if err := tx.Model(&model.User{}).Count(&users).Error; err != nil {
				return err
			}
			if users == 0 {
				if err := bootstrap(tx); err != nil {
					return err
				}
			}
		}
		return tx.Create(&model.Version{Version: version}).Error
	})
}

func Migrate(version uint) error {
	global.Logger.Info("Migrating....", version)
	password := ""
	err := migrateDatabase(global.DB, version, func(tx *gorm.DB) error {
		var err error
		password, err = bootstrapDefaultData(tx)
		return err
	})
	if err == nil && password != "" {
		global.Logger.Info("Admin Password Is: ", password)
	}
	return err
}

func bootstrapDefaultData(tx *gorm.DB) (string, error) {
	localizer := global.Localizer("")
	name, _ := localizer.LocalizeMessage(&i18n.Message{ID: "DefaultGroup"})
	group := model.Group{Name: name, Type: model.GroupTypeDefault}
	if err := tx.Where("name = ? and type = ?", name, model.GroupTypeDefault).FirstOrCreate(&group).Error; err != nil {
		return "", err
	}
	shareName, _ := localizer.LocalizeMessage(&i18n.Message{ID: "ShareGroup"})
	share := model.Group{Name: shareName, Type: model.GroupTypeShare}
	if err := tx.Where("name = ? and type = ?", shareName, model.GroupTypeShare).FirstOrCreate(&share).Error; err != nil {
		return "", err
	}
	enabled := true
	password := utils.RandomString(8)
	hash, err := utils.EncryptPassword(password)
	if err != nil {
		return "", err
	}
	admin := model.User{Username: "admin", Nickname: "Admin", Status: model.COMMON_STATUS_ENABLE, IsAdmin: &enabled, GroupId: group.Id, Password: hash}
	if err := tx.Create(&admin).Error; err != nil {
		return "", err
	}
	return password, nil
}
