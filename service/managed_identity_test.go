package service

import (
	"errors"
	"sync"
	"testing"

	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

func TestManagedCreateAndEditRejectIdentityCollisions(t *testing.T) {
	db := managedIdentityDB(t)
	ps := &PeerService{}
	original := model.Peer{Id: "first", Uuid: "same", Alias: "keep", GroupId: 3, UserId: 7}
	createIdentityRow(t, db, &original)
	for _, peer := range []model.Peer{{Id: "first", Uuid: "other"}, {Id: "other", Uuid: "same"}} {
		if err := ps.Create(&peer); !errors.Is(err, ErrPeerIdentityConflict) {
			t.Fatalf("admin create accepted conflict: %v", err)
		}
	}
	other := model.Peer{Id: "second", Uuid: "second-uuid"}
	createIdentityRow(t, db, &other)
	for _, edit := range []model.Peer{{RowId: original.RowId, Id: "second", Uuid: "same"}, {RowId: original.RowId, Id: "first", Uuid: "changed"}} {
		if err := ps.UpdateByAdmin(&edit); !errors.Is(err, ErrPeerIdentityConflict) {
			t.Fatalf("admin edit accepted conflicting identity: %v", err)
		}
	}
	var after model.Peer
	db.First(&after, original.RowId)
	if after.Id != "first" || after.Uuid != "same" || after.UserId != 7 {
		t.Fatal("conflict partially updated inventory")
	}
}

func TestManagedEditPreservesOwnerAndMigratesExactTokens(t *testing.T) {
	db := managedIdentityDB(t)
	ps := &PeerService{}
	original := model.Peer{Id: "old", Uuid: "same", Alias: "clear", GroupId: 3, UserId: 7}
	createIdentityRow(t, db, &original)
	token := model.UserToken{DeviceId: "old", DeviceUuid: "same", Token: "token"}
	createIdentityRow(t, db, &token)
	if err := ps.UpdateByAdmin(&model.Peer{RowId: original.RowId, Id: "new", Uuid: "same"}); err != nil {
		t.Fatal(err)
	}
	var after model.Peer
	db.First(&after, original.RowId)
	if after.Id != "new" || after.UserId != 7 || after.Alias != "" || after.GroupId != 0 {
		t.Fatal("admin edit lost ownership or ignored zero fields")
	}
	var moved model.UserToken
	db.First(&moved, token.Id)
	if moved.DeviceId != "new" {
		t.Fatal("admin rename did not migrate token")
	}
}

func TestManagedCreateRacesWithDeviceReport(t *testing.T) {
	db := managedIdentityDB(t)
	ps := &PeerService{}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errs <- ps.Create(&model.Peer{Id: "shared", Uuid: "admin"}) }()
	go func() {
		defer wg.Done()
		_, err := ps.UpsertSysInfo(&model.Peer{Id: "shared", Uuid: "client"})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrPeerIdentityConflict) {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&model.Peer{}).Count(&count)
	if count != 1 || successes != 1 {
		t.Fatalf("competing writers created %d records, %d successes", count, successes)
	}
}

func TestDeleteLegacyPeerKeepsWebTokens(t *testing.T) {
	db := managedIdentityDB(t)
	peer := model.Peer{Id: "legacy"}
	createIdentityRow(t, db, &peer)
	token := model.UserToken{DeviceUuid: "", Token: "web"}
	createIdentityRow(t, db, &token)
	if err := (&PeerService{}).Delete(&peer); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.UserToken{}).Count(&count)
	if count != 1 {
		t.Fatal("deleting legacy device revoked web tokens")
	}
}

func TestDeletePeerRollsBackOnTokenFailure(t *testing.T) {
	db := managedIdentityDB(t)
	peer := model.Peer{Id: "device", Uuid: "same"}
	createIdentityRow(t, db, &peer)
	if err := db.Callback().Delete().Before("gorm:delete").Register("fail_token_delete", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_tokens" {
			tx.AddError(errors.New("token delete failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	createIdentityRow(t, db, &model.UserToken{DeviceUuid: "same", Token: "active"})
	if err := (&PeerService{}).Delete(&peer); err == nil {
		t.Fatal("token failure ignored")
	}
	var count int64
	db.Model(&model.Peer{}).Count(&count)
	if count != 1 {
		t.Fatal("failed delete partially removed device")
	}
}

func TestLogoutClearsExactOwnedBindingsAndRevokesToken(t *testing.T) {
	db := managedIdentityDB(t)
	peers := []model.Peer{{Id: "a", Uuid: "same", UserId: 7}, {Id: "b", Uuid: "same", UserId: 7}, {Id: "c", Uuid: "same", UserId: 8}}
	createIdentityRow(t, db, &peers)
	createIdentityRow(t, db, &model.UserToken{UserId: 7, DeviceUuid: "same", Token: "logout"})
	if err := (&UserService{}).Logout(&model.User{IdModel: model.IdModel{Id: 7}}, "logout"); err != nil {
		t.Fatal(err)
	}
	var after []model.Peer
	db.Order("row_id").Find(&after)
	if after[0].UserId != 0 || after[1].UserId != 0 || after[2].UserId != 8 {
		t.Fatal("logout selected an arbitrary or unowned duplicate")
	}
}

func managedIdentityDB(t *testing.T) *gorm.DB {
	db := identityDB(t)
	old := AllService
	AllService = &Service{PeerService: &PeerService{}, UserService: &UserService{}}
	t.Cleanup(func() { AllService = old })
	return db
}
