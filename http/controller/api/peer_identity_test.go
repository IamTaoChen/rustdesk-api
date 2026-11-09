package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func reportRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.Peer{}, &model.UserToken{}, &model.LoginLog{}, &model.PeerIdentityLock{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldServices := service.DB, service.AllService
	service.DB = db
	service.AllService = &service.Service{PeerService: &service.PeerService{}, UserService: &service.UserService{}}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.DB = oldDB; service.AllService = oldServices; sqlDB.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/sysinfo", (&Peer{}).SysInfo)
	router.POST("/api/heartbeat", (&Index{}).Heartbeat)
	return router, db
}

func postIdentity(t *testing.T, router *gin.Engine, path string, value interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSysInfoHTTPRenameAndConflict(t *testing.T) {
	router, db := reportRouter(t)
	old := model.Peer{Id: "old", Uuid: "same", Alias: "preserve", UserId: 7, GroupId: 3}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	reply := postIdentity(t, router, "/api/sysinfo", map[string]string{"id": "new", "uuid": "same", "hostname": "renamed"})
	if reply.Code != 200 || reply.Body.String() != "SYSINFO_UPDATED" {
		t.Fatalf("unexpected response: %d %s", reply.Code, reply.Body.String())
	}
	var rows []model.Peer
	db.Find(&rows)
	if len(rows) != 1 || rows[0].RowId != old.RowId || rows[0].Id != "new" || rows[0].Alias != "preserve" || rows[0].GroupId != 3 || rows[0].UserId != 7 {
		t.Fatal("rename lost device identity")
	}
	reply = postIdentity(t, router, "/api/sysinfo", map[string]string{"id": "new", "uuid": "different"})
	if reply.Code != 409 {
		t.Fatalf("ID collision accepted: %d", reply.Code)
	}
	reply = postIdentity(t, router, "/api/sysinfo", map[string]string{"id": "new"})
	if reply.Code != 400 {
		t.Fatalf("missing UUID accepted: %d", reply.Code)
	}
}

func TestHeartbeatHTTPRequestsSysInfoForStaleIdentity(t *testing.T) {
	router, db := reportRouter(t)
	peer := model.Peer{Id: "current", Uuid: "same"}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]string{{"id": "old", "uuid": "same"}, {"id": "current", "uuid": "wrong"}, {"id": "unknown", "uuid": "new"}} {
		reply := postIdentity(t, router, "/api/heartbeat", payload)
		var body map[string]interface{}
		if err := json.Unmarshal(reply.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if reply.Code != 200 || body["sysinfo"] != true {
			t.Fatalf("stale heartbeat did not request upload: %d %s", reply.Code, reply.Body.String())
		}
	}
	var after model.Peer
	db.First(&after, peer.RowId)
	if after.LastOnlineTime != 0 {
		t.Fatal("wrong identity updated online status")
	}
	reply := postIdentity(t, router, "/api/heartbeat", map[string]string{"id": "current", "uuid": "same"})
	if reply.Code != 200 || reply.Body.String() != "{}" {
		t.Fatalf("matching heartbeat changed protocol: %d %s", reply.Code, reply.Body.String())
	}
	db.First(&after, peer.RowId)
	if after.LastOnlineTime == 0 {
		t.Fatal("matching heartbeat was not recorded")
	}
}
