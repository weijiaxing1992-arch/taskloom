package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// serverMonitor 是企业管理员查看本机运行风险的只读快照。它绝不回传会话密钥、
// webhook 密钥、完整环境变量或数据库绝对路径，避免把运维页变成凭据泄露面。
type serverMonitor struct {
	CollectedAt string                `json:"collectedAt"`
	Status      string                `json:"status"`
	Alerts      []serverMonitorAlert  `json:"alerts"`
	Server      serverMonitorServer   `json:"server"`
	Host        serverMonitorHost     `json:"host"`
	Process     serverMonitorProcess  `json:"process"`
	Database    serverMonitorDatabase `json:"database"`
}

type serverMonitorAlert struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Metric string `json:"metric,omitempty"`
}

type serverMonitorServer struct {
	StartedAt       string `json:"startedAt"`
	UptimeSeconds   int64  `json:"uptimeSeconds"`
	Address         string `json:"address"`
	OperatingSystem string `json:"operatingSystem"`
	Architecture    string `json:"architecture"`
	GoVersion       string `json:"goVersion"`
	WebRoot         string `json:"webRoot"`
}

type serverMonitorHost struct {
	Hostname string              `json:"hostname"`
	CPUCores int                 `json:"cpuCores"`
	Load1    *float64            `json:"load1,omitempty"`
	Memory   serverMonitorMemory `json:"memory"`
	Disk     serverMonitorDisk   `json:"disk"`
}

type serverMonitorMemory struct {
	TotalBytes     int64 `json:"totalBytes"`
	AvailableBytes int64 `json:"availableBytes"`
}

type serverMonitorDisk struct {
	TotalBytes     int64  `json:"totalBytes"`
	AvailableBytes int64  `json:"availableBytes"`
	Path           string `json:"path"`
}

type serverMonitorProcess struct {
	PID              int    `json:"pid"`
	Goroutines       int    `json:"goroutines"`
	HeapAllocBytes   uint64 `json:"heapAllocBytes"`
	HeapSystemBytes  uint64 `json:"heapSystemBytes"`
	OpenConnections  int    `json:"openConnections"`
	InUseConnections int    `json:"inUseConnections"`
	RequestCapacity  int    `json:"requestCapacity"`
	InFlightRequests int    `json:"inFlightRequests"`
}

type serverMonitorDatabase struct {
	FileName    string `json:"fileName"`
	FileBytes   int64  `json:"fileBytes"`
	WALBytes    int64  `json:"walBytes"`
	PingMillis  int64  `json:"pingMillis"`
	QuickCheck  string `json:"quickCheck"`
	ForeignKeys bool   `json:"foreignKeys"`
	Synchronous string `json:"synchronous"`
}

// 监控入口只允许企业管理员访问。普通组织权限即使包含 reports.view，也不能读取
// 主机资源、监听配置等运维信息；代访问同样沿用外层只读限制和真实身份校验。
func (a *App) serverMonitor(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	_, admin, err := a.organizationAccess(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if !admin {
		fail(w, 403, "server_monitor_admin_required", "仅企业管理员可以查看服务器监控")
		return
	}
	// 采集最多占用两秒；任何一个非关键指标不可用时降级为“未知”，不会让监控页
	// 因某个系统命令或文件系统的偶发错误完全失效。
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	write(w, 200, a.serverMonitorSnapshot(ctx))
}

func (a *App) serverMonitorSnapshot(ctx context.Context) serverMonitor {
	now := time.Now().UTC()
	startedAt := a.startedAt
	if startedAt.IsZero() {
		startedAt = now
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	dbStats := a.db.Stats()
	requestCapacity, inFlight := cap(a.apiSlots), len(a.apiSlots)
	if requestCapacity > 0 && inFlight > 0 {
		// 当前监控请求自身已占据一个限流槽，不把它误报为业务压力。
		inFlight--
	}
	database := a.serverMonitorDatabaseSnapshot(ctx)
	memory := hostMemorySnapshot(ctx)
	disk := diskSnapshot(a.databasePath)
	load := hostLoadOne(ctx)
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	snapshot := serverMonitor{
		CollectedAt: now.Format(time.RFC3339),
		Status:      "healthy",
		Alerts:      []serverMonitorAlert{},
		Server: serverMonitorServer{
			StartedAt:       startedAt.Format(time.RFC3339),
			UptimeSeconds:   maxInt64(0, int64(now.Sub(startedAt).Seconds())),
			Address:         env("DEVFLOW_ADDR", ":8080"),
			OperatingSystem: runtime.GOOS,
			Architecture:    runtime.GOARCH,
			GoVersion:       runtime.Version(),
			WebRoot:         filepath.Base(strings.TrimRight(a.web, string(os.PathSeparator))),
		},
		Host: serverMonitorHost{
			Hostname: hostname,
			CPUCores: runtime.NumCPU(),
			Load1:    load,
			Memory:   memory,
			Disk:     disk,
		},
		Process: serverMonitorProcess{
			PID:              os.Getpid(),
			Goroutines:       runtime.NumGoroutine(),
			HeapAllocBytes:   mem.HeapAlloc,
			HeapSystemBytes:  mem.HeapSys,
			OpenConnections:  dbStats.OpenConnections,
			InUseConnections: dbStats.InUse,
			RequestCapacity:  requestCapacity,
			InFlightRequests: inFlight,
		},
		Database: database,
	}
	snapshot.Alerts = monitorAlerts(snapshot)
	for _, alert := range snapshot.Alerts {
		if alert.Level == "critical" {
			snapshot.Status = "critical"
			break
		}
		if alert.Level == "warning" {
			snapshot.Status = "warning"
		}
	}
	return snapshot
}

func (a *App) serverMonitorDatabaseSnapshot(ctx context.Context) serverMonitorDatabase {
	path := a.databasePath
	fileName := "未配置"
	if path != "" {
		fileName = filepath.Base(path)
	}
	result := serverMonitorDatabase{FileName: fileName, QuickCheck: "unknown", Synchronous: strings.ToUpper(env("DEVFLOW_SQLITE_SYNCHRONOUS", "NORMAL"))}
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			result.FileBytes = info.Size()
		}
		if info, err := os.Stat(path + "-wal"); err == nil {
			result.WALBytes = info.Size()
		}
	}
	started := time.Now()
	err := a.db.PingContext(ctx)
	result.PingMillis = time.Since(started).Milliseconds()
	if err != nil {
		result.QuickCheck = "unavailable"
		return result
	}
	var quickCheck string
	if err = a.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&quickCheck); err != nil {
		result.QuickCheck = "unavailable"
		return result
	}
	// 损坏详情可能含表名或业务值；运维 API 只公开检查结论。
	result.QuickCheck = "failed"
	if strings.EqualFold(strings.TrimSpace(quickCheck), "ok") {
		result.QuickCheck = "ok"
	}
	var foreignKeys int
	if err = a.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err == nil {
		result.ForeignKeys = foreignKeys == 1
	}
	return result
}

func diskSnapshot(databasePath string) serverMonitorDisk {
	path := "."
	if databasePath != "" {
		path = filepath.Dir(databasePath)
	}
	result := serverMonitorDisk{Path: filepath.Base(path)}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil || stat.Bsize == 0 {
		return result
	}
	result.TotalBytes = byteCount(stat.Blocks, uint64(stat.Bsize))
	result.AvailableBytes = byteCount(stat.Bavail, uint64(stat.Bsize))
	return result
}

func byteCount(blocks, blockSize uint64) int64 {
	if blockSize == 0 || blocks > uint64(^uint64(0))/blockSize {
		return 0
	}
	value := blocks * blockSize
	if value > uint64(^uint64(0)>>1) {
		return 0
	}
	return int64(value)
}

func hostMemorySnapshot(ctx context.Context) serverMonitorMemory {
	if runtime.GOOS == "linux" {
		if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
			return linuxMemorySnapshot(string(raw))
		}
	}
	if runtime.GOOS == "darwin" {
		return darwinMemorySnapshot(ctx)
	}
	return serverMonitorMemory{}
}

func linuxMemorySnapshot(raw string) serverMonitorMemory {
	values := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 || !strings.HasSuffix(parts[0], ":") {
			continue
		}
		if value, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
			values[strings.TrimSuffix(parts[0], ":")] = value * 1024
		}
	}
	return serverMonitorMemory{TotalBytes: values["MemTotal"], AvailableBytes: values["MemAvailable"]}
}

func darwinMemorySnapshot(ctx context.Context) serverMonitorMemory {
	result := serverMonitorMemory{}
	totalRaw, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return result
	}
	total, err := strconv.ParseInt(strings.TrimSpace(string(totalRaw)), 10, 64)
	if err != nil || total <= 0 {
		return result
	}
	result.TotalBytes = total
	vmRaw, err := exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
	if err != nil {
		// 没有可用页数时不能把 0 当成真实内存耗尽。
		return serverMonitorMemory{}
	}
	pageSize := int64(4096)
	freePages, inactivePages := int64(0), int64(0)
	for _, line := range strings.Split(string(vmRaw), "\n") {
		if index := strings.Index(line, "page size of "); index >= 0 {
			if fields := strings.Fields(line[index+len("page size of "):]); len(fields) > 0 {
				if parsed, parseErr := strconv.ParseInt(fields[0], 10, 64); parseErr == nil && parsed > 0 {
					pageSize = parsed
				}
			}
		}
		if strings.HasPrefix(line, "Pages free:") {
			freePages = vmStatPages(line)
		}
		if strings.HasPrefix(line, "Pages inactive:") {
			inactivePages = vmStatPages(line)
		}
	}
	// macOS 没有稳定的“MemAvailable”系统字段，这里使用空闲页与可回收的 inactive
	// 页作为保守估算；前端会明确标示为可用内存估算，而非内存压力判断。
	result.AvailableBytes = maxInt64(0, (freePages+inactivePages)*pageSize)
	return result
}

func vmStatPages(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return 0
	}
	value := strings.TrimSuffix(parts[len(parts)-1], ".")
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func hostLoadOne(ctx context.Context) *float64 {
	var raw string
	if runtime.GOOS == "linux" {
		if value, err := os.ReadFile("/proc/loadavg"); err == nil {
			raw = string(value)
		}
	} else if runtime.GOOS == "darwin" {
		if value, err := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "vm.loadavg").Output(); err == nil {
			raw = string(value)
		}
	}
	raw = strings.Trim(strings.TrimSpace(raw), "{}")
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return nil
	}
	return &value
}

func monitorAlerts(snapshot serverMonitor) []serverMonitorAlert {
	alerts := []serverMonitorAlert{}
	appendCapacityAlert := func(level, title, detail, metric string) {
		alerts = append(alerts, serverMonitorAlert{Level: level, Title: title, Detail: detail, Metric: metric})
	}
	if snapshot.Database.QuickCheck != "ok" {
		appendCapacityAlert("critical", "数据库完整性检查异常", "SQLite quick_check 未返回 ok，请立即备份并执行人工核查。", "database.quickCheck")
	}
	if !snapshot.Database.ForeignKeys {
		appendCapacityAlert("critical", "数据库外键约束未启用", "当前连接未启用 SQLite foreign_keys，关联数据可能失去保护。", "database.foreignKeys")
	}
	if snapshot.Database.PingMillis > 500 {
		appendCapacityAlert("critical", "数据库响应过慢", fmt.Sprintf("数据库 Ping 已达 %d ms。", snapshot.Database.PingMillis), "database.pingMillis")
	} else if snapshot.Database.PingMillis > 100 {
		appendCapacityAlert("warning", "数据库响应偏慢", fmt.Sprintf("数据库 Ping 已达 %d ms，建议观察锁等待和磁盘 I/O。", snapshot.Database.PingMillis), "database.pingMillis")
	}
	appendResourceAlert(&alerts, "磁盘可用空间不足", "磁盘可用空间低于阈值，数据库 WAL 与备份可能无法继续写入。", "host.disk", snapshot.Host.Disk.AvailableBytes, snapshot.Host.Disk.TotalBytes)
	appendResourceAlert(&alerts, "可用内存不足", "可用内存估算低于阈值，建议检查常驻进程与并发负载。", "host.memory", snapshot.Host.Memory.AvailableBytes, snapshot.Host.Memory.TotalBytes)
	if snapshot.Host.Load1 != nil && snapshot.Host.CPUCores > 0 {
		ratio := *snapshot.Host.Load1 / float64(snapshot.Host.CPUCores)
		if ratio >= 1.5 {
			appendCapacityAlert("critical", "CPU 负载偏高", fmt.Sprintf("1 分钟负载 %.2f，已达到 %d 个逻辑核心的 %.0f%%。", *snapshot.Host.Load1, snapshot.Host.CPUCores, ratio*100), "host.load1")
		} else if ratio >= 1.0 {
			appendCapacityAlert("warning", "CPU 负载偏高", fmt.Sprintf("1 分钟负载 %.2f，接近 %d 个逻辑核心的处理能力。", *snapshot.Host.Load1, snapshot.Host.CPUCores), "host.load1")
		}
	}
	if snapshot.Process.RequestCapacity > 0 {
		ratio := float64(snapshot.Process.InFlightRequests) / float64(snapshot.Process.RequestCapacity)
		if ratio >= .9 {
			appendCapacityAlert("warning", "请求处理槽接近上限", fmt.Sprintf("当前 %d / %d 个业务请求并发执行。", snapshot.Process.InFlightRequests, snapshot.Process.RequestCapacity), "process.inFlightRequests")
		}
	}
	return alerts
}

func appendResourceAlert(alerts *[]serverMonitorAlert, title, detail, metric string, available, total int64) {
	if total <= 0 || available < 0 {
		return
	}
	ratio := float64(available) / float64(total)
	if ratio < .10 {
		*alerts = append(*alerts, serverMonitorAlert{Level: "critical", Title: title, Detail: detail, Metric: metric})
	} else if ratio < .20 {
		*alerts = append(*alerts, serverMonitorAlert{Level: "warning", Title: title, Detail: detail, Metric: metric})
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
