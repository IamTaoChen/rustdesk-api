package service

import (
	"github.com/lejianwen/rustdesk-api/v2/model"
	"gorm.io/gorm"
)

type PeerService struct {
}

// FindById 根据id查找
func (ps *PeerService) FindById(id string) *model.Peer {
	p := &model.Peer{}
	DB.Where("id = ?", id).First(p)
	return p
}
func (ps *PeerService) FindByUuid(uuid string) *model.Peer {
	p := &model.Peer{}
	DB.Where("uuid = ?", uuid).First(p)
	return p
}
func (ps *PeerService) InfoByRowId(id uint) *model.Peer {
	p := &model.Peer{}
	DB.Where("row_id = ?", id).First(p)
	return p
}

// FindByUserIdAndUuid 根据用户id和uuid查找peer
func (ps *PeerService) FindByUserIdAndUuid(uuid string, userId uint) *model.Peer {
	p := &model.Peer{}
	DB.Where("uuid = ? and user_id = ?", uuid, userId).First(p)
	return p
}

// UuidBindUserId 绑定用户id
func (ps *PeerService) UuidBindUserId(deviceId string, uuid string, userId uint) error {
	if !validPeerIdentity(deviceId,uuid) {return ErrInvalidPeerIdentity}
	return DB.Transaction(func(tx *gorm.DB)error{
		if err:=LockPeerIdentity(tx);err!=nil {return err}
		return bindExactPeerUser(tx,deviceId,uuid,userId)
	})
}

func (ps *PeerService) UuidUnbindUserId(uuid string, userId uint) error {
	return DB.Transaction(func(tx *gorm.DB)error{
		if err:=LockPeerIdentity(tx);err!=nil {return err}
		return clearExactPeerUser(tx,uuid,userId)
	})
}

// EraseUserId 清除用户id, 用于用户删除
func (ps *PeerService) EraseUserId(userId uint) error {
	return DB.Model(&model.Peer{}).Where("user_id = ?", userId).Update("user_id", 0).Error
}

// ListByUserIds 根据用户id取列表
func (ps *PeerService) ListByUserIds(userIds []uint, page, pageSize uint) (res *model.PeerList) {
	res = &model.PeerList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.Peer{})
	tx.Where("user_id in (?)", userIds)
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.Peers)
	return
}

func (ps *PeerService) List(page, pageSize uint, where func(tx *gorm.DB)) (res *model.PeerList) {
	res = &model.PeerList{}
	res.Page = int64(page)
	res.PageSize = int64(pageSize)
	tx := DB.Model(&model.Peer{})
	if where != nil {
		where(tx)
	}
	tx.Count(&res.Total)
	tx.Scopes(Paginate(page, pageSize))
	tx.Find(&res.Peers)
	return
}

// ListFilterByUserId 根据用户id过滤Peer列表
func (ps *PeerService) ListFilterByUserId(page, pageSize uint, where func(tx *gorm.DB), userId uint) (res *model.PeerList) {
	userWhere := func(tx *gorm.DB) {
		tx.Where("user_id = ?", userId)
		// 如果还有额外的筛选条件，执行它
		if where != nil {
			where(tx)
		}
	}
	return ps.List(page, pageSize, userWhere)
}

// Create 创建
func (ps *PeerService) Create(u *model.Peer) error {
	return createManagedPeer(u)
}

func (ps *PeerService) Delete(u *model.Peer) error {
	if u==nil || u.RowId==0 {return ErrInvalidPeerIdentity}
	return deleteManagedPeers([]uint{u.RowId})
}

// GetUuidListByIDs 根据ids获取uuid列表
func (ps *PeerService) GetUuidListByIDs(ids []uint) ([]string, error) {
	var uuids []string
	err := DB.Model(&model.Peer{}).
		Where("row_id in (?)", ids).
		Pluck("uuid", &uuids).Error
	//过滤uuids中的空字符串
	var newUuids []string
	for _, uuid := range uuids {
		if uuid != "" {
			newUuids = append(newUuids, uuid)
		}
	}
	return newUuids, err
}

// BatchDelete 批量删除, 同时也应该删除token
func (ps *PeerService) BatchDelete(ids []uint) error {
	return deleteManagedPeers(ids)
}

// Update 更新
func (ps *PeerService) Update(u *model.Peer) error {
	return DB.Model(u).Updates(u).Error
}

// UpdateByAdmin 后台编辑设备：只更新表单里的字段，并且允许写入零值。
// Updates(struct) 会跳过零值，导致 group_id=0（移出分组）和清空 alias 无法保存。
// UpdateByAdmin updates only the admin-editable fields and writes zero values too:
// Updates(struct) skips zero values, so moving a peer out of its group (group_id=0)
// or clearing the alias never reached the database.
func (ps *PeerService) UpdateByAdmin(u *model.Peer) error {
	return updateManagedPeer(u)
}
