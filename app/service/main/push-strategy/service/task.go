package service

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	filtermdl "go-common/app/service/main/filter/model/rpc"
	"go-common/app/service/main/push-strategy/dao"
	"go-common/app/service/main/push-strategy/model"
	pushmdl "go-common/app/service/main/push/model"
	"go-common/library/ecode"
	"go-common/library/log"
	"go-common/library/sync/errgroup"
)

const (
	_retry = 3
)

func (s *Service) saveMid(v *model.MidChan) (err error) {
	select {
	case s.midCh <- v:
	default:
		dao.PromError("mid chan full")
		err = ecode.PushServiceBusyErr
		s.cache.Save(func() { s.dao.SendWechat("mid chan full") })
	}
	log.Info("mid chan len(%d)", len(s.midCh))
	return
}

func (s *Service) saveMidproc() {
	defer s.wg.Done()
	for {
		v, ok := <-s.midCh
		if !ok {
			log.Info("saveMidproc() closed")
			return
		}
		log.Info("saveMidproc consume job(%d)", v.Task.Job)
		s.handleTask(v)
		log.Info("saveMidproc done job(%d)", v.Task.Job)
	}
}

func (s *Service) handleTask(mch *model.MidChan) (err error) {
	var (
		path  string
		mids  []string
		mlock sync.Mutex
		group = errgroup.Group{}
		lines = strings.Split(*mch.Data, ",")
	)
	n := len(lines) / s.c.Cfg.HandleTaskGoroutines
	for i := 1; i <= s.c.Cfg.HandleTaskGoroutines; i++ {
		end := n
		if i == s.c.Cfg.HandleTaskGoroutines {
			end = len(lines)
		}
		part := lines[:end]
		lines = lines[end:]
		group.Go(func() (e error) {
			var list []string
			for _, v := range part {
				mid := strings.Trim(v, " \n\t\r")
				valid := s.checkMid(mch.Task.APPID, mch.Task.BusinessID, mid)
				if !valid {
					dao.PromInfo("task:filtered mid")
					continue
				}
				list = append(list, mid)
			}
			if len(list) > 0 {
				mlock.Lock()
				mids = append(mids, list...)
				mlock.Unlock()
			}
			return nil
		})
	}
	group.Wait()
	if len(mids) == 0 {
		log.Info("handleTask(%+v) no valid mid", mch.Task)
		return
	}
	if path, err = s.saveMids(mids); err != nil {
		log.Error("handleTask(%+v) saveMids(%d) error(%v)", mch.Task, len(mids), err)
		s.cache.Save(func() { s.dao.SendWechat(fmt.Sprintf("handleTask(%d) saveMid error(%v)", mch.Task.Job, err)) })
		return
	}
	mch.Task.MidFile = path
	if err = s.saveTask(mch.Task); err != nil {
		log.Error("handleTask(%+v) saveTask error(%v)", mch.Task, err)
		s.cache.Save(func() { s.dao.SendWechat(fmt.Sprintf("handleTask(%d) saveTask error(%v)", mch.Task.Job, err)) })
	}
	return
}

func (s *Service) saveMids(mids []string) (path string, err error) {
	name := strconv.FormatInt(time.Now().UnixNano(), 10) + mids[0]
	data := []byte(strings.Join(mids, "\n"))
	for i := 0; i < _retry; i++ {
		if path, err = s.saveNASFile(name, data); err == nil {
			break
		}
		dao.PromError("retry saveNASFile")
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		s.dao.SendWechat("saveMids error:" + err.Error())
	}
	return
}

func (s *Service) saveTask(t *pushmdl.Task) (err error) {
	for i := 0; i < _retry; i++ {
		if _, err = s.dao.AddTask(context.Background(), t); err == nil {
			break
		}
		dao.PromError("retry saveTask")
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		s.dao.SendWechat("saveTask error:" + err.Error())
	}
	return
}

func (s *Service) checkMid(app, biz int64, midStr string) (valid bool) {
	mid, _ := strconv.ParseInt(midStr, 10, 64)
	if mid == 0 {
		log.Warn("limited, mid(%s) parse error", midStr)
		return
	}
	// ??????????????????
	if !s.checkMidBySetting(int(biz), mid) {
		return
	}
	// ??????????????????????????????
	if biz == 42 {
		return true
	}
	// ??????CD??????
	if !s.checkMidByCDTime(app, biz, mid) {
		return
	}
	// ??????????????????
	if !s.checkMidByCount(app, biz, mid) {
		return
	}
	// ??????CD????????????
	if err := s.cache.Save(func() { s.dao.AddCDCache(context.Background(), app, mid) }); err != nil {
		log.Error("add cd cache app(%d) biz(%d) mid(%d) error(%v)", app, biz, mid, err)
	}
	log.Info("passed, app(%d) biz(%d) mid(%d)", app, biz, mid)
	return true
}

// ????????????????????????????????????????????????
// ????????????????????? false, ?????????????????????????????????????????????????????? true
func (s *Service) checkMidBySetting(biz int, mid int64) bool {
	st, ok := s.settings[mid]
	if !ok || st == nil {
		return true
	}
	if biz != s.c.BizID.Live && biz != s.c.BizID.Archive {
		return true
	}
	var skey int
	switch biz {
	case s.c.BizID.Live:
		skey = pushmdl.UserSettingLive
	case s.c.BizID.Archive:
		skey = pushmdl.UserSettingArchive
	default:
		return true
	}
	if i, ok := st[skey]; ok && i == pushmdl.SwitchOff {
		log.Info("limited, mid(%d) switch off biz(%d)", mid, biz)
		return false
	}
	return true
}

// ??????????????????????????????????????????????????????????????????
// ?????????????????? false??????????????????????????????????????? true
func (s *Service) checkMidByCount(app, biz, mid int64) bool {
	var (
		err          error
		countDay     int
		countBiz     int
		countNotLive int
		day          = time.Now().Format("20060102")
	)
	// ??????????????????????????????????????????
	for i := 0; i < _retry; i++ {
		if countBiz, err = s.dao.IncrLimitBizCache(context.Background(), day, app, mid, biz); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		log.Error("s.dao.IncrLimitBizCache(%s,%d,%d,%d) error(%v)", day, app, mid, biz, err)
		return true
	}
	if countBiz > s.businesses[biz].PushLimitUser {
		log.Info("limited, mid(%d) app(%d) business(%d) more than business limit, current(%d)", mid, app, biz, countBiz)
		return false
	}
	// TODO ??????????????????????????????????????????????????????
	// !! ????????????APP???????????????????????????????????? !!
	// ????????????4????????????,????????????????????????????????????
	if app == pushmdl.APPIDBBPhone && biz != int64(s.c.BizID.Live) {
		for i := 0; i < _retry; i++ {
			if countNotLive, err = s.dao.IncrLimitNotLiveCache(context.Background(), day, mid); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if err != nil {
			log.Error("s.dao.IncrLimitNotLiveCache(%s,%d) error(%v)", day, mid, err)
			return true
		}
		if countNotLive > s.apps[app].PushLimitUser-4 {
			log.Info("limited, mid(%d) more than live remain", mid)
			return false
		}
	}
	// ?????????????????????????????????
	for i := 0; i < _retry; i++ {
		if countDay, err = s.dao.IncrLimitDayCache(context.Background(), day, app, mid); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		log.Error("s.dao.IncrLimitDayCache(%s,%d,%d) error(%v)", day, app, mid, err)
		return true
	}
	if countDay > s.apps[app].PushLimitUser {
		log.Warn("limited, mid(%d) app(%d) more than day limit, current(%d)", mid, app, countDay)
		return false
	}
	return true
}

// ??????????????????????????????
// ??????????????????????????????????????????????????????????????????????????????
func (s *Service) checkMidByCDTime(app, biz, mid int64) bool {
	// ????????????????????????
	if s.businesses[biz].Whitelist == pushmdl.SwitchOn {
		return true
	}
	exist, err := s.dao.ExistsCDCache(context.Background(), app, mid)
	if err != nil {
		log.Error("s.dao.ExistsCDCache(%d,%d) error(%v)", app, mid, err)
		return true
	}
	// ???cd?????????
	if exist {
		log.Info("limited, app(%d) business(%d) mid(%d) in cd time", app, biz, mid)
		return false
	}
	return true
}

// AddTask adds task.
func (s *Service) AddTask(c context.Context, uuid, token string, task *pushmdl.Task, mids string) (job int64, err error) {
	if err = s.checkBusiness(task.BusinessID, token); err != nil {
		log.Warn("checkBusiness task(%+v) error(%v)", task, err)
		return
	}
	var exist bool
	if exist, err = s.dao.ExistsUUIDCache(c, task.BusinessID, uuid); err == nil && exist {
		log.Warn("AddTask(%d,%s,%s) uuid limited", task.BusinessID, task.Title, task.LinkValue)
		err = ecode.PushUUIDErr
		return
	}
	s.dao.AddUUIDCache(c, task.BusinessID, uuid)
	// filter sensitive words in title & content and check uuid
	group, errCtx := errgroup.WithContext(c)
	group.Go(func() error {
		if filtered, e := s.filter(errCtx, task.Title); e == nil && filtered != task.Title {
			log.Error("AddTask(%s) title(%s) contains sensitive words(%s)", task.LinkValue, task.Title, filtered)
			return ecode.PushSensitiveWordsErr
		}
		return nil
	})
	group.Go(func() error {
		if filtered, e := s.filter(errCtx, task.Summary); e == nil && filtered != task.Summary {
			log.Error("AddTask(%s) content(%s) contains sensitive words(%s)", task.LinkValue, task.Summary, filtered)
			return ecode.PushSensitiveWordsErr
		}
		return nil
	})
	if err = group.Wait(); err != nil {
		s.dao.DelUUIDCache(c, task.BusinessID, uuid)
		return
	}
	b := s.businesses[task.BusinessID]
	task.APPID = b.APPID
	task.Sound = b.Sound
	task.Vibration = b.Vibration
	if err = s.saveMid(&model.MidChan{Task: task, Data: &mids}); err != nil {
		return
	}
	dao.PromInfo("task:????????????")
	return task.Job, nil
}

func (s *Service) checkBusiness(id int64, token string) error {
	b, ok := s.businesses[id]
	if !ok {
		log.Error("business is not exist. business(%d) token(%s)", id, token)
		dao.PromError("service:??????????????????")
		return ecode.PushBizAuthErr
	}
	if token != b.Token {
		log.Error("wrong token business(%d) token(%s) need(%s)", id, token, b.Token)
		dao.PromError("service:?????????token??????")
		return ecode.PushBizAuthErr
	}
	if b.PushSwitch == pushmdl.SwitchOff {
		log.Error("business was forbidden. business(%d) token(%s)", id, token)
		dao.PromError("service:????????????????????????")
		return ecode.PushBizForbiddenErr
	}
	// ?????????????????????
	if inSilence(b.SilentTime) {
		log.Warn("in silent time, forbidden. business(%d)", id)
		return ecode.PushSilenceErr
	}
	return nil
}

func inSilence(st pushmdl.BusinessSilentTime) bool {
	if st.BeginHour == st.EndHour && st.BeginMinute == st.EndMinute {
		return false
	}
	now := time.Now()
	stm := time.Date(now.Year(), now.Month(), now.Day(), st.BeginHour, st.BeginMinute, 0, 0, time.Local)
	etm := time.Date(now.Year(), now.Month(), now.Day(), st.EndHour, st.EndMinute, 59, 999, time.Local)
	if st.BeginHour > st.EndHour || (st.BeginHour == st.EndHour && st.BeginMinute > st.EndMinute) {
		etm = time.Date(now.Year(), now.Month(), now.Day(), st.EndHour, st.EndMinute, 59, 999, time.Local).Add(24 * time.Hour)
	}
	if now.Unix() >= stm.Unix() && now.Unix() <= etm.Unix() {
		return true
	}
	return false
}

// Filter filters sensitive words.
func (s *Service) filter(c context.Context, content string) (res string, err error) {
	var (
		filterRes *filtermdl.FilterRes
		arg       = filtermdl.ArgFilter{Area: "common", Message: content}
	)
	if filterRes, err = s.filterRPC.Filter(c, &arg); err != nil {
		dao.PromError("push:????????????")
		log.Error("s.filter(%s) error(%v)", content, err)
		return
	}
	if filterRes.Level < 20 {
		return content, nil
	}
	res = filterRes.Result
	return
}

// saveNASFile writes data into NAS.
func (s *Service) saveNASFile(name string, data []byte) (path string, err error) {
	name = fmt.Sprintf("%x", md5.Sum([]byte(name)))
	dir := fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(s.c.Cfg.NASPath, "/"), time.Now().Format("20060102"), name[:2])
	if _, err = os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			log.Error("os.IsNotExist(%s) error(%v)", dir, err)
			return
		}
		if err = os.MkdirAll(dir, 0777); err != nil {
			log.Error("os.MkdirAll(%s) error(%v)", dir, err)
			return
		}
	}
	path = fmt.Sprintf("%s/%s", dir, name)
	f, err := os.OpenFile(path, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Error("s.saveNASFile(%s) OpenFile() error(%v)", path, err)
		return
	}
	if _, err = f.Write(data); err != nil {
		log.Error("s.saveNASFile(%s) f.Write() error(%v)", err)
		return
	}
	if err = f.Close(); err != nil {
		log.Error("s.saveNASFile(%s) f.Close() error(%v)", err)
	}
	return
}
