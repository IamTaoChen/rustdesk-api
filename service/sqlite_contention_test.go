package service

import (
	"sync"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

// Start another real writer after the request's first read. A deferred read
// transaction cannot safely upgrade once that writer has acquired the database.
func concurrentSQLiteWriter(t *testing.T, db *gorm.DB, table string, run func() error) {
	t.Helper()
	peer := model.Peer{Id: "writer", Uuid: "writer-uuid"}
	createIdentityRow(t, db, &peer)
	start, done := make(chan struct{}), make(chan error, 1)
	go func() {
		<-start
		done <- db.Model(&model.Peer{}).Where("row_id = ?", peer.RowId).Update("hostname", "concurrent").Error
	}()
	var once sync.Once
	var writerErr error
	finished := false
	if err := db.Callback().Query().After("gorm:query").Register("test_concurrent_writer", func(tx *gorm.DB) {
		if tx.Statement.Table != table {
			return
		}
		once.Do(func() {
			close(start)
			select {
			case writerErr = <-done:
				finished = true
			case <-time.After(100 * time.Millisecond):
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	err := run()
	db.Callback().Query().Remove("test_concurrent_writer")
	// Unblock cleanup even if the operation failed before its first read.
	once.Do(func() { close(start) })
	if !finished {
		writerErr = <-done
	}
	if writerErr != nil {
		t.Fatalf("concurrent writer failed: %v", writerErr)
	}
	if err != nil {
		t.Fatalf("request failed during concurrent write: %v", err)
	}
}

func TestAddressBookSyncWithConcurrentWriter(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			db := identityDB(t)
			if err := db.Exec("PRAGMA journal_mode = " + mode).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.AddressBook{}); err != nil {
				t.Fatal(err)
			}
			createIdentityRow(t, db, &model.Peer{Id: "new", Uuid: "new-uuid", Os: "Linux", Username: "desktop", Hostname: "computer"})
			createIdentityRow(t, db, &[]model.AddressBook{{Id: "keep", UserId: 7, Alias: "old"}, {Id: "remove", UserId: 7}, {Id: "other-user", UserId: 8}})
			concurrentSQLiteWriter(t, db, "address_books", func() error {
				return (&AddressBookService{}).UpdateAddressBook([]*model.AddressBook{{Id: "keep", Alias: "updated"}, {Id: "new"}}, 7)
			})
			var rows []model.AddressBook
			if err := db.Order("id").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[0].Id != "keep" || rows[0].Alias != "updated" || rows[1].Id != "new" || rows[1].Hostname != "computer" || rows[1].Username != "desktop" || rows[1].Platform != "Linux" || rows[2].Id != "other-user" || rows[2].UserId != 8 {
				t.Fatalf("address-book sync lost writes or changed another user: %#v", rows)
			}
		})
	}
}

func TestTagSyncWithConcurrentWriter(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			db := identityDB(t)
			if err := db.Exec("PRAGMA journal_mode = " + mode).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.Tag{}); err != nil {
				t.Fatal(err)
			}
			createIdentityRow(t, db, &[]model.Tag{{Name: "keep", UserId: 7, Color: 1}, {Name: "remove", UserId: 7}, {Name: "other-user", UserId: 8, Color: 9}})
			concurrentSQLiteWriter(t, db, "tags", func() error {
				return (&TagService{}).UpdateTags(7, map[string]uint{"keep": 2, "new": 3})
			})
			var rows []model.Tag
			if err := db.Order("name").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[0].Name != "keep" || rows[0].Color != 2 || rows[1].Name != "new" || rows[1].Color != 3 || rows[2].Name != "other-user" || rows[2].Color != 9 || rows[2].UserId != 8 {
				t.Fatalf("tag sync lost writes or changed another user: %#v", rows)
			}
		})
	}
}
