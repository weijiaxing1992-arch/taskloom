package main

import (
	"net/http"
	"testing"
)

func TestServerMonitorRequiresTenantAdministrator(t *testing.T) {
	a := testApp(t)
	viewer := apiRequest(a, http.MethodGet, "/api/organization/server-monitor", "u_viewer", "unrelated-project", "")
	if viewer.Code != http.StatusForbidden {
		t.Fatalf("non-admin accessed server monitor: %d %s", viewer.Code, viewer.Body.String())
	}
	admin := apiRequest(a, http.MethodGet, "/api/organization/server-monitor", "u_admin", "unrelated-project", "")
	if admin.Code != http.StatusOK {
		t.Fatalf("admin monitor request failed: %d %s", admin.Code, admin.Body.String())
	}
	result := jsonMap(t, admin)
	if result["status"] == "" || result["collectedAt"] == "" {
		t.Fatalf("monitor snapshot misses status: %#v", result)
	}
	server, ok := result["server"].(map[string]any)
	if !ok || server["operatingSystem"] == "" || server["goVersion"] == "" {
		t.Fatalf("monitor server config missing: %#v", result["server"])
	}
	database, ok := result["database"].(map[string]any)
	if !ok || database["quickCheck"] != "ok" || database["foreignKeys"] != true {
		t.Fatalf("monitor database health is invalid: %#v", result["database"])
	}
}

func TestMonitorParsersAndResourceThresholds(t *testing.T) {
	memory := linuxMemorySnapshot("MemTotal:       1000 kB\nMemAvailable:    150 kB\n")
	if memory.TotalBytes != 1000*1024 || memory.AvailableBytes != 150*1024 {
		t.Fatalf("linux memory parse failed: %#v", memory)
	}
	if vmStatPages("Pages free:                               123.") != 123 {
		t.Fatal("vm_stat page parser failed")
	}
	alerts := monitorAlerts(serverMonitor{Database: serverMonitorDatabase{QuickCheck: "ok", ForeignKeys: true}, Host: serverMonitorHost{Disk: serverMonitorDisk{TotalBytes: 100, AvailableBytes: 9}, Memory: serverMonitorMemory{TotalBytes: 100, AvailableBytes: 19}}})
	if len(alerts) != 2 || alerts[0].Level != "critical" || alerts[1].Level != "warning" {
		t.Fatalf("resource thresholds unexpected: %#v", alerts)
	}
}
