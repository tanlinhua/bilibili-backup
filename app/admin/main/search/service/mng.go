package service

import (
	"context"
	"encoding/json"
	"strconv"

	"go-common/app/admin/main/search/model"
	"go-common/library/ecode"
	"go-common/library/sync/errgroup"
)

// BusinessList .
func (s *Service) BusinessList(ctx context.Context, name string, pn, ps int) (list []*model.MngBusiness, total int64, err error) {
	offset := (pn - 1) * ps
	if list, err = s.dao.BusinessList(ctx, name, offset, ps); err != nil {
		return
	}
	total, err = s.dao.BusinessTotal(ctx, name)
	return
}

// BusinessAll .
func (s *Service) BusinessAll(ctx context.Context) (list []*model.MngBusiness, err error) {
	list, err = s.dao.BusinessAll(ctx)
	return
}

// BusinessInfo .
func (s *Service) BusinessInfo(ctx context.Context, id int64) (info *model.MngBusiness, err error) {
	info, err = s.dao.BusinessInfo(ctx, id)
	return
}

// AddBusiness .
func (s *Service) AddBusiness(ctx context.Context, b *model.MngBusiness) (id int64, err error) {
	info, err := s.dao.BusinessInfoByName(ctx, b.Name)
	if err != nil {
		return
	}
	if info != nil {
		err = ecode.SearchBusinessExistErr
		return
	}
	id, err = s.dao.AddBusiness(ctx, b)
	return
}

// UpdateBusiness .
func (s *Service) UpdateBusiness(ctx context.Context, b *model.MngBusiness) (err error) {
	err = s.dao.UpdateBusiness(ctx, b)
	return
}

// UpdateBusinessApp .
func (s *Service) UpdateBusinessApp(ctx context.Context, business, app, incrWay string, isJob, incrOpen bool) (err error) {
	info, err := s.dao.BusinessInfoByName(ctx, business)
	if err != nil {
		return
	}
	var exist bool
	for k, v := range info.Apps {
		if v.AppID == app {
			exist = true
			if !isJob {
				info.Apps = append(info.Apps[:k], info.Apps[k+1:]...)
				break
			}
			v.IncrWay = incrWay
			v.IncrOpen = incrOpen
		}
	}
	if !exist {
		info.Apps = append(info.Apps, &model.MngBusinessApp{AppID: app, IncrWay: incrWay, IncrOpen: incrOpen})
	}
	bs, err := json.Marshal(info.Apps)
	if err != nil {
		return
	}
	info.AppsJSON = string(bs)
	err = s.dao.UpdateBusiness(ctx, info)
	return
}

// AssetList .
func (s *Service) AssetList(ctx context.Context, typ int, name string, pn, ps int) (list []*model.MngAsset, total int64, err error) {
	offset := (pn - 1) * ps
	if list, err = s.dao.AssetList(ctx, typ, name, offset, ps); err != nil {
		return
	}
	total, err = s.dao.AssetTotal(ctx, typ, name)
	return
}

// AssetAll .
func (s *Service) AssetAll(ctx context.Context) (list []*model.MngAsset, err error) {
	list, err = s.dao.AssetAll(ctx)
	return
}

// AssetInfo .
func (s *Service) AssetInfo(ctx context.Context, id int64) (info *model.MngAsset, err error) {
	info, err = s.dao.AssetInfo(ctx, id)
	return
}

// AddAsset .
func (s *Service) AddAsset(ctx context.Context, a *model.MngAsset) (id int64, err error) {
	info, err := s.dao.AssetInfoByName(ctx, a.Name)
	if err != nil {
		return
	}
	if info != nil {
		err = ecode.SearchAssetExistErr
		return
	}
	id, err = s.dao.AddAsset(ctx, a)
	return
}

// UpdateAsset .
func (s *Service) UpdateAsset(ctx context.Context, a *model.MngAsset) (err error) {
	if err = s.dao.UpdateAsset(ctx, a); err != nil {
		return
	}
	if a.Type == model.MngAssetTypeDatabus {
		if a.Config == "" {
			return
		}
		v := new(model.MngAssetDatabus)
		if err = json.Unmarshal([]byte(a.Config), &v); err != nil {
			return
		}
		err = s.dao.UpdateAppAssetDatabus(ctx, a.Name, v)
		return
	}
	if a.Type == model.MngAssetTypeTable {
		if a.Config == "" {
			return
		}
		v := new(model.MngAssetTable)
		if err = json.Unmarshal([]byte(a.Config), &v); err != nil {
			return
		}
		err = s.dao.UpdateAppAssetTable(ctx, a.Name, v)
		return
	}
	return
}

// AppList .
func (s *Service) AppList(ctx context.Context, business string) (list []*model.MngApp, err error) {
	list, err = s.dao.AppList(ctx, business)
	return
}

// AppInfo .
func (s *Service) AppInfo(ctx context.Context, id int64) (info *model.MngApp, err error) {
	info, err = s.dao.AppInfo(ctx, id)
	return
}

// AddApp .
func (s *Service) AddApp(ctx context.Context, a *model.MngApp) (id int64, err error) {
	info, err := s.dao.AppInfoByAppid(ctx, a.AppID)
	if err != nil {
		return
	}
	if info != nil {
		err = ecode.SearchAssetExistErr
		return
	}
	id, err = s.dao.AddApp(ctx, a)
	return
}

// UpdateApp .
func (s *Service) UpdateApp(ctx context.Context, a *model.MngApp) (err error) {
	group := errgroup.Group{}
	group.Go(func() error {
		if a.TableName == "" {
			a.TableFormat = ""
			a.TablePrefix = ""
			return nil
		}
		tb, e := s.dao.AssetInfoByName(ctx, a.TableName)
		if e != nil {
			return e
		}
		if tb == nil || tb.Config == "" {
			return nil
		}
		val := new(model.MngAssetTable)
		if e := json.Unmarshal([]byte(tb.Config), val); e != nil {
			return e
		}
		a.TablePrefix = val.TablePrefix
		a.TableFormat = val.TableFormat
		return nil
	})
	group.Go(func() error {
		if a.DatabusName == "" {
			a.DatabusInfo = ""
			a.DatabusIndexID = ""
			return nil
		}
		dbus, e := s.dao.AssetInfoByName(ctx, a.DatabusName)
		if e != nil {
			return e
		}
		if dbus == nil || dbus.Config == "" {
			return nil
		}
		val := new(model.MngAssetDatabus)
		if e := json.Unmarshal([]byte(dbus.Config), val); e != nil {
			return e
		}
		a.DatabusInfo = val.DatabusInfo
		a.DatabusIndexID = val.DatabusIndexID
		return nil
	})
	if err = group.Wait(); err != nil {
		return
	}
	err = s.dao.UpdateApp(ctx, a)
	return
}

// MngCountList .
func (s *Service) MngCountList(ctx context.Context) (list []*model.MngCount, err error) {
	daily := "????????????"
	sum := "????????????"
	list = []*model.MngCount{
		// ?????????
		{Business: "?????????", Type: sum, Name: "?????????????????????", Chart: "line", Param: "business=app&type=all"},
		{Business: "?????????", Type: daily, Name: "?????????????????????", Chart: "line", Param: "business=app&type=inc"},
		// ??????+??????
		{Business: "????????????", Type: daily, Name: "archive????????????", Chart: "line", Param: "business=archive&type=inc"},
		{Business: "????????????", Type: daily, Name: "video????????????", Chart: "line", Param: "business=archive_video&type=inc"},
		{Business: "????????????", Type: sum, Name: "archive????????????", Chart: "line", Param: "business=archive&type=all"},
		{Business: "????????????", Type: sum, Name: "video????????????", Chart: "line", Param: "business=archive_video&type=all"},
		// ??????
		{Business: "??????", Type: daily, Name: "??????????????????", Chart: "line", Param: "business=dm&type=inc"},
		{Business: "??????", Type: daily, Name: "????????????????????????", Chart: "line", Param: "business=dm_report&type=inc"},
		{Business: "??????", Type: daily, Name: "????????????????????????", Chart: "line", Param: "business=dm_monitor&type=inc"},
		{Business: "??????", Type: sum, Name: "??????????????????", Chart: "line", Param: "business=dm&type=all"},
		{Business: "??????", Type: sum, Name: "????????????????????????", Chart: "line", Param: "business=dm_report&type=all"},
		{Business: "??????", Type: sum, Name: "????????????????????????", Chart: "line", Param: "business=dm_monitor&type=all"},
		// ??????
		{Business: "??????", Type: daily, Name: "??????????????????", Chart: "line", Param: "business=reply&type=inc"},
		// ??????
		{Business: "??????", Type: "????????????", Name: "???????????????????????????", Chart: "line", Param: "business=log_audit_access&type=inc"},
		{Business: "??????", Type: "????????????", Name: "?????????????????????????????? - ????????????", Chart: "pie", Param: "business=log_audit_business&type=inc"},
		{Business: "??????", Type: "????????????", Name: "?????????????????????????????? - ????????????", Chart: "pie", Param: "business=log_audit_uid&type=inc"},
		{Business: "??????", Type: "????????????", Name: "???????????????????????????", Chart: "line", Param: "business=log_user_action_access&type=inc"},
		{Business: "??????", Type: "????????????", Name: "?????????????????????????????? - ????????????", Chart: "pie", Param: "business=log_user_action_business&type=inc"},
		{Business: "??????", Type: "????????????", Name: "?????????????????????????????? - ????????????", Chart: "pie", Param: "business=log_user_action_uid&type=inc"},
		// ??????
		{Business: "??????", Type: sum, Name: "??????????????????", Chart: "line", Param: "business=user&type=all"},
		// ??????
		{Business: "??????", Type: daily, Name: "??????????????????", Chart: "line", Param: "business=article&type=inc"},
		{Business: "??????", Type: sum, Name: "??????????????????", Chart: "line", Param: "business=article&type=all"},
	}
	return list, err
}

// MngCount .
func (s *Service) MngCount(ctx context.Context, c *model.MngCount) (list []*model.MngCountRes, err error) {
	list, err = s.dao.MngCount(ctx, c)
	return
}

// MngCount .
func (s *Service) MngPercent(ctx context.Context, c *model.MngCount) (list []*model.MngPercentRes, err error) {
	list, err = s.dao.MngPercent(ctx, c)
	switch c.Business {
	case "log_audit_business":
		for k, v := range list {
			if id, e := strconv.Atoi(v.Name); e == nil {
				if t, ok := s.dao.GetLogInfo("log_audit", id); ok {
					list[k].Name = t.Name
				}
			}
		}
	case "log_user_action_business":
		for k, v := range list {
			if id, e := strconv.Atoi(v.Name); e == nil {
				if t, ok := s.dao.GetLogInfo("log_user_action", id); ok {
					list[k].Name = t.Name
				}
			}
		}
	case "log_audit_uid", "log_user_action_uid":
		uid := []string{}
		for _, v := range list {
			uid = append(uid, v.Name)
		}
		if data, err := s.dao.Unames(ctx, uid); err == nil {
			for k, v := range list {
				if t, ok := data.Data[v.Name]; ok {
					list[k].Name = t
				}
			}
		}
	}
	return
}
