package service

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func identityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")+"?_busy_timeout=10000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Peer{}, &model.UserToken{}, &model.LoginLog{}, &model.PeerIdentityLock{}); err != nil {
		t.Fatal(err)
	}
	old := DB
	DB = db
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { DB = old; sqlDB.Close() })
	return db
}

func createIdentityRow(t *testing.T, db *gorm.DB, row interface{}) {
	t.Helper()
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSysInfoRenamePreservesDeviceAndMatchingTokens(t *testing.T) {
	db := identityDB(t)
	peer := model.Peer{Id: "old-device", Uuid: "opaque/base64==", UserId: 7, GroupId: 4, Alias: "managed", Hostname: "old", LastOnlineIp: "192.0.2.1", LastOnlineTime: 100}
	createIdentityRow(t, db, &peer)
	tokens := []model.UserToken{{DeviceId: peer.Id, DeviceUuid: peer.Uuid, Token: "matched", UserId: 7}, {DeviceId: peer.Id, DeviceUuid: "other", Token: "different-uuid", UserId: 9}}
	createIdentityRow(t, db, &tokens)
	log := model.LoginLog{DeviceId: peer.Id, Uuid: peer.Uuid, UserId: 7}
	createIdentityRow(t, db, &log)
	ps := &PeerService{}
	got, err := ps.UpsertSysInfo(&model.Peer{Id: "new-device", Uuid: peer.Uuid, Hostname: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if got.RowId != peer.RowId {
		t.Fatal("rename replaced row identity")
	}
	var rows []model.Peer
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d device rows", len(rows))
	}
	p := rows[0]
	if p.Id != "new-device" || p.Hostname != "new" || p.UserId != 7 || p.GroupId != 4 || p.Alias != "managed" || p.LastOnlineTime != 100 || p.LastOnlineIp != "192.0.2.1" || !time.Time(p.CreatedAt).Equal(time.Time(peer.CreatedAt)) {
		t.Fatalf("device metadata was lost: %#v", p)
	}
	var after []model.UserToken
	db.Order("id").Find(&after)
	if after[0].DeviceId != "new-device" || after[1].DeviceId != "old-device" {
		t.Fatal("token identity migration mismatch")
	}
	var history model.LoginLog
	db.First(&history, log.Id)
	if history.DeviceId != "old-device" {
		t.Fatal("historical login was rewritten")
	}
}

func TestSysInfoConflictsLeaveRecordsUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		rows   []model.Peer
		report model.Peer
	}{
		{"id belongs to another UUID", []model.Peer{{Id: "target", Uuid: "other"}}, model.Peer{Id: "target", Uuid: "mine"}},
		{"rename target occupied", []model.Peer{{Id: "old", Uuid: "mine"}, {Id: "target", Uuid: "other"}}, model.Peer{Id: "target", Uuid: "mine"}},
		{"duplicate UUID", []model.Peer{{Id: "old", Uuid: "mine"}, {Id: "other", Uuid: "mine"}}, model.Peer{Id: "target", Uuid: "mine"}},
		{"duplicate ID", []model.Peer{{Id: "target", Uuid: "mine"}, {Id: "target", Uuid: "other"}}, model.Peer{Id: "target", Uuid: "mine"}},
		{"known UUID plus legacy row", []model.Peer{{Id: "old", Uuid: "mine"}, {Id: "target"}}, model.Peer{Id: "target", Uuid: "mine"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := identityDB(t)
			createIdentityRow(t, db, &tc.rows)
			_, err := (&PeerService{}).UpsertSysInfo(&tc.report)
			if !errors.Is(err, ErrPeerIdentityConflict) {
				t.Fatalf("expected identity conflict, got %v", err)
			}
			var after []model.Peer
			db.Order("row_id").Find(&after)
			if len(after) != len(tc.rows) {
				t.Fatal("conflict changed row count")
			}
			for i, p := range after {
				if p.Id != tc.rows[i].Id || p.Uuid != tc.rows[i].Uuid {
					t.Fatal("conflict changed identity")
				}
			}
		})
	}
}

func TestSysInfoLegacyAndZeroValues(t *testing.T) {
	db := identityDB(t)
	peer := model.Peer{Id: "legacy", Username: "old user", UserId: 5, GroupId: 2, Alias: "keep"}
	createIdentityRow(t, db, &peer)
	ps := &PeerService{}
	got, err := ps.UpsertSysInfo(&model.Peer{Id: peer.Id, Uuid: "opaque-uuid"})
	if err != nil {
		t.Fatal(err)
	}
	if got.RowId != peer.RowId || got.Uuid != "opaque-uuid" || got.Username != "" || got.UserId != 5 || got.GroupId != 2 || got.Alias != "keep" {
		t.Fatalf("legacy adoption or empty metadata failed: %#v", got)
	}
	token := model.UserToken{DeviceUuid: "new-uuid", DeviceId: "new", UserId: 8, ExpiredAt: time.Now().Add(time.Hour).Unix()}
	createIdentityRow(t, db, &token)
	createIdentityRow(t, db, &model.LoginLog{Uuid: "new-uuid", DeviceId: "new", UserId: 8, UserTokenId: token.Id})
	got, err = ps.UpsertSysInfo(&model.Peer{Id: "new", Uuid: "new-uuid"})
	if err != nil || got.UserId != 8 {
		t.Fatalf("new-device login association failed: %v", err)
	}
}

func TestSysInfoInvalidIdentityDoesNotWrite(t *testing.T) {
	db := identityDB(t)
	for _, p := range []model.Peer{{Id: "valid"}, {Uuid: "valid"}, {Id: " valid", Uuid: "u"}, {Id: "valid", Uuid: "u\n"}, {Id: strings.Repeat("x", 254), Uuid: "u"}, {Id: "valid", Uuid: strings.Repeat("u", 1025)}, {Id: "bad\x00id", Uuid: "u"}} {
		if _, err := (&PeerService{}).UpsertSysInfo(&p); !errors.Is(err, ErrInvalidPeerIdentity) {
			t.Fatalf("invalid identity accepted: %v", err)
		}
	}
	var count int64
	db.Model(&model.Peer{}).Count(&count)
	if count != 0 {
		t.Fatal("invalid identity created a peer")
	}
}

func TestHeartbeatChecksBothIdentityFieldsAndThrottle(t *testing.T) {
	db := identityDB(t)
	ps := &PeerService{}
	peer := model.Peer{Id: "current", Uuid: "same", LastOnlineTime: 100, LastOnlineIp: "192.0.2.1"}
	createIdentityRow(t, db, &peer)
	for _, pair := range [][2]string{{"current", "wrong"}, {"old", "same"}, {"unknown", "other"}} {
		found, err := ps.Heartbeat(pair[0], pair[1], "192.0.2.2", 200)
		if err != nil || found {
			t.Fatalf("mismatched heartbeat accepted: %v", err)
		}
	}
	found, err := ps.Heartbeat("current", "same", "192.0.2.2", 110)
	if err != nil || !found {
		t.Fatal(err)
	}
	var after model.Peer
	db.First(&after, peer.RowId)
	if after.LastOnlineTime != 100 {
		t.Fatal("heartbeat throttle ignored")
	}
	_, err = ps.Heartbeat("current", "same", "192.0.2.2", 130)
	if err != nil {
		t.Fatal(err)
	}
	db.First(&after, peer.RowId)
	if after.LastOnlineTime != 130 || after.LastOnlineIp != "192.0.2.2" {
		t.Fatal("matching heartbeat not updated")
	}
	if _, err = ps.Heartbeat("current", "", "192.0.2.2", 200); !errors.Is(err, ErrInvalidPeerIdentity) {
		t.Fatal("empty UUID accepted")
	}
}

func TestConcurrentSysInfoCreatesOneDevice(t *testing.T) {
	db := identityDB(t)
	const n = 12
	results := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := (&PeerService{}).UpsertSysInfo(&model.Peer{Id: "shared", Uuid: "same"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&model.Peer{}).Count(&count)
	if count != 1 {
		t.Fatalf("got %d rows", count)
	}
}

func TestSysInfoTokenFailureRollsBackRename(t *testing.T) {
	db := identityDB(t)
	peer := model.Peer{Id: "old", Uuid: "same"}
	createIdentityRow(t, db, &peer)
	if err := db.Migrator().DropTable(&model.UserToken{}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&PeerService{}).UpsertSysInfo(&model.Peer{Id: "new", Uuid: "same"}); err == nil {
		t.Fatal("token write failure ignored")
	}
	var after model.Peer
	db.First(&after, peer.RowId)
	if after.Id != "old" {
		t.Fatal("failed transaction partially renamed peer")
	}
}

func TestHeartbeatRejectsCaseFoldedIdentity(t *testing.T) {
	db := identityDB(t)
	// Exercise a case-insensitive schema, as commonly used by MySQL deployments.
	if err := db.Exec("DROP TABLE peers").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE peers (row_id integer PRIMARY KEY, id text COLLATE NOCASE, uuid text COLLATE NOCASE, last_online_time integer DEFAULT 0, last_online_ip text, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO peers (id,uuid) VALUES (?,?)", "Device", "AbCd==").Error; err != nil {
		t.Fatal(err)
	}
	found, err := (&PeerService{}).Heartbeat("Device", "abcd==", "192.0.2.1", 100)
	if err != nil || found {
		t.Fatalf("case-folded UUID was treated as exact identity: %v", err)
	}
	found, err = (&PeerService{}).Heartbeat("device", "AbCd==", "192.0.2.1", 100)
	if err != nil || found {
		t.Fatalf("case-folded ID was treated as exact identity: %v", err)
	}
}

func TestSysInfoRejectsCaseFoldedUUID(t *testing.T) {
	db := identityDB(t)
	if err := db.Exec("DROP TABLE peers").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE peers (row_id integer PRIMARY KEY, id text COLLATE NOCASE, uuid text COLLATE NOCASE, user_id integer DEFAULT 0)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO peers (id,uuid,user_id) VALUES (?,?,?)", "Device", "AbCd==", 7).Error; err != nil {
		t.Fatal(err)
	}
	_, err := (&PeerService{}).UpsertSysInfo(&model.Peer{Id: "new", Uuid: "abcd=="})
	if !errors.Is(err, ErrPeerIdentityConflict) {
		t.Fatalf("case-folded UUID was treated as existing device: %v", err)
	}
}

func TestSysInfoUsesLatestExactLoginIdentity(t *testing.T) {
	db := identityDB(t)
	if err := db.Exec("DROP TABLE login_logs").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE login_logs (id integer PRIMARY KEY, uuid text COLLATE NOCASE, device_id text COLLATE NOCASE, user_id integer, user_token_id integer)").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		uuid, id string
		user     uint
	}{{"AbCd==", "Device", 7}, {"abcd==", "Device", 9}, {"AbCd==", "device", 10}} {
		token := model.UserToken{DeviceUuid: row.uuid, DeviceId: row.id, UserId: row.user, ExpiredAt: time.Now().Add(time.Hour).Unix()}
		createIdentityRow(t, db, &token)
		if err := db.Exec("INSERT INTO login_logs (uuid,device_id,user_id,user_token_id) VALUES (?,?,?,?)", row.uuid, row.id, row.user, token.Id).Error; err != nil {
			t.Fatal(err)
		}
	}
	peer, err := (&PeerService{}).UpsertSysInfo(&model.Peer{Id: "Device", Uuid: "AbCd=="})
	if err != nil {
		t.Fatal(err)
	}
	if peer.UserId != 7 {
		t.Fatalf("expected latest exact login user 7, got %d", peer.UserId)
	}
}

func TestSysInfoMigratesOnlyExactTokenIdentity(t *testing.T) {
	db := identityDB(t)
	if err := db.Exec("DROP TABLE user_tokens").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE user_tokens (id integer PRIMARY KEY, device_uuid text COLLATE NOCASE, device_id text COLLATE NOCASE, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	createIdentityRow(t, db, &model.Peer{Id: "Device", Uuid: "AbCd==", UserId: 7})
	for _, pair := range [][2]string{{"AbCd==", "Device"}, {"abcd==", "Device"}, {"AbCd==", "device"}} {
		if err := db.Exec("INSERT INTO user_tokens (device_uuid,device_id) VALUES (?,?)", pair[0], pair[1]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (&PeerService{}).UpsertSysInfo(&model.Peer{Id: "new", Uuid: "AbCd=="}); err != nil {
		t.Fatal(err)
	}
	var tokens []model.UserToken
	if err := db.Order("id").Find(&tokens).Error; err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 || tokens[0].DeviceId != "new" || tokens[1].DeviceId != "Device" || tokens[2].DeviceId != "device" {
		t.Fatal("rename migrated another exact token identity")
	}
}

func TestHeartbeatLinearizesWithCaseOnlyRename(t *testing.T) {
	db := identityDB(t)
	if err := db.Exec("DROP TABLE peers").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE peers (row_id integer PRIMARY KEY, id text COLLATE NOCASE, uuid text COLLATE NOCASE, last_online_time integer DEFAULT 0, last_online_ip text, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO peers (row_id,id,uuid) VALUES (1,?,?)", "Device", "same").Error; err != nil {
		t.Fatal(err)
	}
	start, attempted, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		<-start
		close(attempted)
		result <- db.Exec("UPDATE peers SET id = ? WHERE row_id = 1", "device").Error
		close(done)
	}()
	var once sync.Once
	committedBeforeValidationCompleted := false
	if err := db.Callback().Query().After("gorm:query").Register("test_case_rename", func(tx *gorm.DB) {
		if tx.Statement.Table != "peers" {
			return
		}
		once.Do(func() {
			close(start)
			<-attempted
			select {
			case <-done:
				committedBeforeValidationCompleted = true
			case <-time.After(100 * time.Millisecond):
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("test_case_rename")
	_, heartbeatErr := (&PeerService{}).Heartbeat("Device", "same", "192.0.2.1", 100)
	<-done
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var peer model.Peer
	if err := db.First(&peer, 1).Error; err != nil {
		t.Fatal(err)
	}
	if committedBeforeValidationCompleted && peer.LastOnlineTime != 0 {
		t.Fatal("stale case-only identity wrote online state after rename committed")
	}
	// SQLite may abort a read-to-write lock upgrade. That is safe: it must roll back.
	if heartbeatErr != nil && peer.LastOnlineTime != 0 {
		t.Fatal("failed heartbeat partially updated the device")
	}
}
