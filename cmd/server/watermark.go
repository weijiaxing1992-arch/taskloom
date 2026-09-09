package main

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// watermarkPeerIP 仅使用服务端连接地址。未配置可信反代边界时，不相信客户端
// 自报的 X-Forwarded-For / X-Real-IP，也不向第三方服务发送账号或网络信息。
func watermarkPeerIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = strings.Trim(remote, "[]")
	}
	// 链路本地 IPv6 的 zone 是网卡标识，不属于需要展示的 IP 地址。
	if index := strings.LastIndex(host, "%"); index >= 0 {
		host = host[:index]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// 水印属于当前会话，不依赖所选项目；姓名始终从有效账号读取，不接受请求体覆盖。
// 这是页面溯源提示，不是不可移除的防泄漏机制；数据访问仍由 API 鉴权控制。
func (a *App) watermark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	var name string
	if err := a.db.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=? AND active=1`, tenantID, a.uid()).Scan(&name); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "水印信息暂时无法读取，请稍后重试")
		return
	}
	write(w, http.StatusOK, map[string]any{
		"userId": a.uid(), "accountName": name, "serverTime": time.Now().UTC().Format(time.RFC3339),
		"ipAddress": watermarkPeerIP(r.RemoteAddr), "ipSource": "connection", "refreshSeconds": 60,
	})
}
