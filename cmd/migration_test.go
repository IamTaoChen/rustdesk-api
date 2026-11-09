package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func migrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

func TestMigrationSchemaFailureDoesNotAdvanceVersion(t *testing.T) {
	db := migrationDB(t)
	if err := db.AutoMigrate(&model.Version{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Version{Version: 265}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("PRAGMA query_only=ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(db, 266, nil); err == nil {
		t.Fatal("schema failure ignored")
	}
	db.Exec("PRAGMA query_only=OFF")
	var version model.Version
	if err := db.Last(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.Version != 265 {
		t.Fatal("failed migration advanced version")
	}
}

func TestMigrationBootstrapFailureRollsBackAndRetries(t *testing.T) {
	db := migrationDB(t)
	failure := errors.New("bootstrap failure")
	bootstrap := func(tx *gorm.DB) error {
		if err := tx.Create(&model.User{Username: "initial"}).Error; err != nil {
			return err
		}
		return failure
	}
	if err := migrateDatabase(db, 266, bootstrap); !errors.Is(err, failure) {
		t.Fatalf("bootstrap failure lost: %v", err)
	}
	for _, table := range []interface{}{&model.Version{}, &model.User{}} {
		var count int64
		db.Model(table).Count(&count)
		if count != 0 {
			t.Fatal("failed bootstrap partially committed")
		}
	}
	calls := 0
	good := func(tx *gorm.DB) error { calls++; return tx.Create(&model.User{Username: "initial"}).Error }
	if err := migrateDatabase(db, 266, good); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(db, 266, good); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("repeat startup bootstrapped twice")
	}
	if err := db.Migrator().DropTable(&model.PeerIdentityLock{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(db, 266, good); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&model.PeerIdentityLock{}) || calls != 1 {
		t.Fatal("same-version schema repair failed")
	}
}

func TestMigrationVersionWriteFailureRollsBackSeed(t *testing.T) {
	db := migrationDB(t)
	if err := db.Callback().Create().Before("gorm:create").Register("fail_version", func(tx *gorm.DB) {
		if tx.Statement.Table == "versions" {
			tx.AddError(errors.New("version write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := migrateDatabase(db, 266, func(tx *gorm.DB) error { return tx.Create(&model.User{Username: "initial"}).Error })
	if err == nil {
		t.Fatal("version write failure ignored")
	}
	var count int64
	db.Model(&model.User{}).Count(&count)
	if count != 0 {
		t.Fatal("seed committed without version")
	}
}

func TestMigrationRefusesNewerSchema(t *testing.T) {
	db := migrationDB(t)
	if err := db.AutoMigrate(&model.Version{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Version{Version: 267}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(db, 266, nil); err == nil {
		t.Fatal("newer schema accepted")
	}
	if db.Migrator().HasTable(&model.Peer{}) {
		t.Fatal("downgrade attempted schema mutation")
	}
}
