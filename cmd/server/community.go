package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
)

// 仅在全新库的可恢复初始化事务链尾端执行，已有库重启绝不重置人员或口令。
func (a *App) initializeCommunity() error {
	if os.Getenv("TASKLOOM_COMMUNITY") != "1" {
		return nil
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	users := []struct{ id, name, email string }{
		{"u_admin", "Admin", "admin@example.com"}, {"u_member", "周亦安", "zhouyian@example.com"}, {"u_pm", "程知夏", "chengzhixia@example.com"},
		{"u_front", "沈清越", "shenqingyue@example.com"}, {"u_front_lead", "夏景行", "xiajingxing@example.com"}, {"u_back", "陆星河", "luxinghe@example.com"}, {"u_back_lead", "韩书宁", "hanshuning@example.com"},
		{"u_algo", "唐思远", "tangsiyuan@example.com"}, {"u_ui", "许沐晴", "xumuqing@example.com"}, {"u_qa", "苏予晨", "suyuchen@example.com"}, {"u_viewer", "顾南乔", "gunanqiao@example.com"}}
	for _, u := range users {
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return err
		}
		password := base64.RawURLEncoding.EncodeToString(secret[:])
		if u.id == "u_admin" {
			password = "123456"
		}
		hash, e := a.encodePassword(password)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`UPDATE users SET name=?,email=?,password_hash=?,must_change_password=1 WHERE tenant_id=? AND id=?`, u.name, u.email, hash, tenantID, u.id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE tenants SET name='星河示例企业' WHERE id=?`, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// 公开初始口令仍有效时只能绑定回环地址；通过 SSH 隧道完成首次改密再开放 HTTPS。
func (a *App) checkCommunityBind(addr string) error {
	var pending int
	if err := a.db.QueryRow(`SELECT count(*) FROM users WHERE tenant_id=? AND id='u_admin' AND must_change_password=1`, tenantID).Scan(&pending); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if pending > 0 && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("Admin 必须先通过回环地址或 SSH 隧道登录并更改初始化密码，才能绑定非回环地址")
	}
	return nil
}
