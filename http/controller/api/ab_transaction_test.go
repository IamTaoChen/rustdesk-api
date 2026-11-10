package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lejianwen/rustdesk-api/v2/model"
	"github.com/lejianwen/rustdesk-api/v2/service"
)

func TestAddressBookSyncReportsDatabaseFailures(t *testing.T) {
	for _, table := range []string{"address_books", "tags"} {
		t.Run(table, func(t *testing.T) {
			router, db := reportRouter(t)
			if err := db.AutoMigrate(&model.AddressBook{}, &model.Tag{}); err != nil {
				t.Fatal(err)
			}
			service.AllService.AddressBookService = &service.AddressBookService{}
			service.AllService.TagService = &service.TagService{}
			router.Use(func(c *gin.Context) { c.Set("curUser", &model.User{IdModel: model.IdModel{Id: 7}}) })
			router.POST("/api/ab", (&Ab{}).UpAb)
			if err := db.Migrator().DropTable(table); err != nil {
				t.Fatal(err)
			}
			reply := postIdentity(t, router, "/api/ab", map[string]string{"data": `{"peers":[],"tag_colors":"{}"}`})
			if reply.Code != http.StatusInternalServerError {
				t.Fatalf("failed sync returned %d: %s", reply.Code, reply.Body.String())
			}
			if strings.Contains(reply.Body.String(), table) || strings.Contains(reply.Body.String(), "no such table") {
				t.Fatalf("response exposed database details: %s", reply.Body.String())
			}
		})
	}
}
