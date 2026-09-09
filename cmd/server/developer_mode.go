package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
)

// 显式源码开发启动器始终创建全新私有数据库；只公开本次随机管理员密码，
// 其他演示身份各自使用独立且不留存的随机密码。此函数只处理未配置密码的
// 种子账号，不改变已有凭据；普通部署保留原有初始化行为。
func developerSeedPassword(userID string) (string, error) {
	mode, password := os.Getenv("DEVFLOW_DEVELOPER_MODE"), os.Getenv("DEVFLOW_DEVELOPER_PASSWORD")
	if mode == "" && password == "" {
		return seedPassword, nil
	}
	if mode != "isolated" || len(password) < 32 || len(password) > 72 || os.Getenv("DEVFLOW_ADDR") != "127.0.0.1:8080" {
		return "", errors.New("isolated developer mode requires the developer launcher, a fresh random credential and loopback address")
	}
	if userID == "u_admin" {
		return password, nil
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", errors.New("could not generate isolated developer credential")
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}
