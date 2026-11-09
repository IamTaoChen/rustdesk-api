package service

import (
	"errors"
	"testing"
	"time"

	"github.com/lejianwen/rustdesk-api/v2/config"
	"github.com/lejianwen/rustdesk-api/v2/lib/jwt"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

func loginSettings(t *testing.T) {
	oldConfig, oldJwt := Config, Jwt
	Config = &config.Config{}
	Config.App.TokenExpire = time.Hour
	Jwt = jwt.NewJwt("", time.Hour)
	t.Cleanup(func() { Config = oldConfig; Jwt = oldJwt })
}

func TestLoginCommitsTokenLogAndExactBinding(t *testing.T) {
	db := identityDB(t)
	loginSettings(t)
	peer := model.Peer{Id: "Device", Uuid: "same", Alias: "keep"}
	createIdentityRow(t, db, &peer)
	user := model.User{IdModel: model.IdModel{Id: 77}, Username: "user"}
	log := model.LoginLog{DeviceId: peer.Id, Uuid: peer.Uuid}
	token, err := (&UserService{}).Login(&user, &log)
	if err != nil {
		t.Fatal(err)
	}
	if token == nil || token.Id == 0 || log.UserTokenId != token.Id || log.UserId != user.Id {
		t.Fatal("login log did not reference token row")
	}
	var after model.Peer
	db.First(&after, peer.RowId)
	if after.UserId != 77 || after.Id != "Device" || after.Alias != "keep" {
		t.Fatal("binding replaced device metadata")
	}
}

func TestLoginFailuresLeaveNoPartialRows(t *testing.T) {
	for _, step := range []string{"user_tokens", "login_logs", "peers"} {
		t.Run(step, func(t *testing.T) {
			db := identityDB(t)
			loginSettings(t)
			createIdentityRow(t, db, &model.Peer{Id: "Device", Uuid: "same"})
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == step {
					tx.AddError(errors.New("injected write failure"))
				}
			}
			if step == "peers" {
				db.Callback().Update().Before("gorm:update").Register("fail_login", callback)
			} else {
				db.Callback().Create().Before("gorm:create").Register("fail_login", callback)
			}
			token, err := (&UserService{}).Login(&model.User{IdModel: model.IdModel{Id: 7}, Username: "user"}, &model.LoginLog{DeviceId: "Device", Uuid: "same"})
			if err == nil || token != nil {
				t.Fatal("failed login returned token")
			}
			for _, table := range []interface{}{&model.UserToken{}, &model.LoginLog{}} {
				var n int64
				db.Model(table).Count(&n)
				if n != 0 {
					t.Fatal("failed login partially committed")
				}
			}
			var peer model.Peer
			db.First(&peer)
			if peer.UserId != 0 {
				t.Fatal("failed login left a binding")
			}
		})
	}
}

func TestLoginRejectsAmbiguousAndStaleDevice(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "duplicate"}[duplicate], func(t *testing.T) {
			db := identityDB(t)
			loginSettings(t)
			createIdentityRow(t, db, &model.Peer{Id: "current", Uuid: "same"})
			if duplicate {
				createIdentityRow(t, db, &model.Peer{Id: "other", Uuid: "same"})
			}
			token, err := (&UserService{}).Login(&model.User{IdModel: model.IdModel{Id: 7}}, &model.LoginLog{DeviceId: "old", Uuid: "same"})
			if !errors.Is(err, ErrPeerIdentityConflict) || token != nil {
				t.Fatalf("expected identity conflict: %v", err)
			}
			var n int64
			db.Model(&model.UserToken{}).Count(&n)
			if n != 0 {
				t.Fatal("conflicting login created token")
			}
		})
	}
}

func TestLoginAllowsWebAndPendingDeviceReport(t *testing.T) {
	identityDB(t)
	loginSettings(t)
	user := &model.User{IdModel: model.IdModel{Id: 7}, Username: "user"}
	for _, log := range []model.LoginLog{{Client: model.LoginLogClientWebAdmin}, {DeviceId: "new", Uuid: "new-uuid"}, {DeviceId: "legacy"}} {
		if _, err := (&UserService{}).Login(user, &log); err != nil {
			t.Fatal(err)
		}
	}
}
