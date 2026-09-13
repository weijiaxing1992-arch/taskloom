package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAIEndpointCanonicalizationAndUnsafeDestinations(t *testing.T) {
	for raw, want := range map[string]string{
		"https://api.owlai.tech":                      "https://api.owlai.tech/v1",
		"https://api.owlai.tech/":                     "https://api.owlai.tech/v1",
		"https://api.owlai.tech/v1/":                  "https://api.owlai.tech/v1",
		"https://API.OPENAI.COM:443/v1":               aiDefaultBaseURL,
		"https://gateway.example.com:8443/openai/v1/": "https://gateway.example.com:8443/openai/v1",
		"https://api.owlai.tech./api":                 "https://api.owlai.tech/api",
		"https://[2606:4700:4700::1111]/v1":           "https://[2606:4700:4700::1111]/v1",
	} {
		got, err := normalizeAIBaseURL(raw)
		if err != nil || got != want {
			t.Fatalf("normalize %s: %s %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "http://api.owlai.tech", "file:///etc/passwd", "https://localhost", "https://localhost.", "https://a.localhost", "https://a.local", "https://metadata.google.internal", "https://127.0.0.1", "https://10.0.0.1", "https://172.16.1.2", "https://192.168.1.1", "https://169.254.169.254/latest/meta-data", "https://100.100.100.200", "https://[::1]", "https://[::ffff:127.0.0.1]", "https://[fc00::1]", "https://[fe80::1%25eth0]", "https://[64:ff9b::a00:1]", "https://[2002:7f00:1::]", "https://[2001:db8::1]", "https://2130706433", "https://0177.0.0.1", "https://user:secret@api.owlai.tech", "https://api.owlai.tech?key=secret", "https://api.owlai.tech?", "https://api.owlai.tech#secret", "https://api.owlai.tech#", "https://api.owlai.tech/%2e%2e", "https://api.owlai.tech/a/../b", "https://api.owlai.tech/a//b", "https://api.owlai.tech\\@127.0.0.1", "https://api.owlai.tech:0", "https://api.owlai.tech:65536", "https://api.owlai.tech:", "https://api.owlai.tech/white space", "https://.api.owlai.tech", "https://api..owlai.tech"} {
		if got, err := normalizeAIBaseURL(raw); err == nil {
			t.Fatalf("unsafe destination accepted %s -> %s", raw, got)
		}
	}
	for _, raw := range []string{"0.0.0.0", "127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.0.8", "192.0.2.2", "192.88.99.1", "198.18.0.1", "198.51.100.2", "203.0.113.9", "224.0.0.1", "240.0.0.1", "::", "::1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "100::1", "2001::1", "2001:db8::2", "2002:7f00:1::", "3fff::1", "fc00::2", "fe80::2", "ff02::1"} {
		if aiPublicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("non-public IP accepted %s", raw)
		}
	}
}

func TestAIEndpointDNSPinsOnlyApprovedIPsAndRejectsMixedAnswers(t *testing.T) {
	for _, addresses := range [][]net.IPAddr{{{IP: net.ParseIP("127.0.0.1")}}, {{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.2")}}, {{IP: net.ParseIP("::ffff:169.254.169.254")}}, {{IP: net.ParseIP("2606:4700:4700::1111"), Zone: "eth0"}}, {}} {
		calls := 0
		dial := aiPinnedDial("gateway.example.com:443", func(context.Context, string) ([]net.IPAddr, error) { return addresses, nil }, func(context.Context, string, string) (net.Conn, error) {
			calls++
			return nil, errors.New("must not dial")
		})
		if _, err := dial(context.Background(), "tcp", "gateway.example.com:443"); err == nil || calls != 0 {
			t.Fatal("private or ambiguous DNS result reached the network")
		}
	}
	lookups, dials := 0, []string{}
	dial := aiPinnedDial("gateway.example.com:443", func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookups++
		if host != "gateway.example.com" {
			t.Fatal("unexpected resolution target")
		}
		if lookups > 1 {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		dials = append(dials, address)
		left, right := net.Pipe()
		right.Close()
		return left, nil
	})
	conn, err := dial(context.Background(), "tcp", "gateway.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if lookups != 1 || !reflect.DeepEqual(dials, []string{"93.184.216.34:443"}) {
		t.Fatal("hostname was re-resolved instead of pinned")
	}
	if _, err := dial(context.Background(), "tcp", "gateway.example.com:443"); err == nil || len(dials) != 1 {
		t.Fatal("DNS rebind reached private network")
	}
	if _, err := dial(context.Background(), "tcp", "another.example.com:443"); err == nil {
		t.Fatal("alternate host accepted")
	}
}

func TestAIEndpointTransportKeepsTLSAndNeverFollowsRedirects(t *testing.T) {
	var hosts []string
	var hostsMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostsMu.Lock()
		hosts = append(hosts, r.Host)
		hostsMu.Unlock()
		w.Header().Set("Location", "https://127.0.0.1/private")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	a := &App{}
	endpoint, client, err := a.aiRequestClient("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("production transport weakened TLS or enabled a proxy")
	}
	lookups := 0
	transport.DialContext = aiPinnedDial("example.com:443", func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Fatal("not IP pinned")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if response, err := client.Get(endpoint); err == nil {
		response.Body.Close()
		t.Fatal("untrusted TLS certificate accepted")
	}
	hostsMu.Lock()
	gotHosts := append([]string{}, hosts...)
	hostsMu.Unlock()
	if len(gotHosts) != 0 {
		t.Fatal("request sent before TLS verification")
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	hostsMu.Lock()
	gotHosts = append([]string{}, hosts...)
	hostsMu.Unlock()
	if response.StatusCode != 307 || !reflect.DeepEqual(gotHosts, []string{"example.com"}) || lookups != 2 {
		t.Fatal("redirect followed or original TLS hostname not preserved")
	}
}

func aiSettingsVersion(t *testing.T, a *App) int {
	t.Helper()
	w := apiRequest(a, "GET", "/api/organization/ai-settings", "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return int(jsonMap(t, w)["version"].(float64))
}

func setAIGateway(t *testing.T, a *App, base string) {
	t.Helper()
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"baseUrl": base, "reuseKey": true, "expectedVersion": aiSettingsVersion(t, a)}))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestAIEndpointSettingsContractKeyConsentAndLegacyMigration(t *testing.T) {
	a := aiApp(t)
	aiMock(a, func(*http.Request) (*http.Response, error) {
		t.Fatal("saving settings must never call a model")
		return nil, nil
	})
	version := aiSettingsVersion(t, a)
	for _, payload := range []map[string]any{{"baseUrl": "https://api.owlai.tech"}, {"baseUrl": "https://api.owlai.tech", "reuseKey": true}, {"baseUrl": "https://api.owlai.tech", "reuseKey": true, "expectedVersion": version - 1}} {
		w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(payload))
		if w.Code != 409 || aiSettingsVersion(t, a) != version {
			t.Fatal("key reassigned without current explicit consent", w.Code)
		}
	}
	setAIGateway(t, a, "https://api.owlai.tech")
	w := apiRequest(a, "GET", "/api/organization/ai-settings", "u_admin", projectID, "")
	if jsonMap(t, w)["baseUrl"] != "https://api.owlai.tech/v1" || jsonMap(t, w)["endpointMode"] != "custom" || aiSettingsVersion(t, a) != version+1 {
		t.Fatal("custom URL not normalized/versioned")
	}
	for _, user := range []string{"u_front", "u_pm", "u_viewer"} {
		w = apiRequest(a, "PATCH", "/api/organization/ai-settings", user, projectID, `{"baseUrl":"https://other.example.com","reuseKey":true}`)
		if w.Code != 403 {
			t.Fatal("non-enterprise administrator changed endpoint")
		}
	}
	// Changing the key while another administrator has an old confirmation
	// must make that confirmation unusable for redirecting the newer key.
	oldVersion := aiSettingsVersion(t, a)
	gatewayKey := "compatible-gateway-token-without-sk-prefix-12345"
	w = apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"apiKey": gatewayKey, "expectedVersion": oldVersion}))
	if w.Code != 200 {
		t.Fatal("safe non-sk gateway key rejected", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"baseUrl": aiDefaultBaseURL, "reuseKey": true, "expectedVersion": oldVersion}))
	if w.Code != 409 {
		t.Fatal("stale confirmation redirected a newly rotated key")
	}
	var audit string
	a.db.QueryRow(`SELECT group_concat(before_json||after_json) FROM audit_logs WHERE object_type='ai_settings'`).Scan(&audit)
	if strings.Contains(audit, aiTestSecret) || strings.Contains(audit, gatewayKey) || strings.Contains(w.Body.String(), gatewayKey) {
		t.Fatal("key leaked in audit/response")
	}
	var encrypted []byte
	if err := a.db.QueryRow(`SELECT encrypted_key FROM organization_ai_settings WHERE tenant_id=?`, tenantID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	bulkFixtureExec(t, a, `ALTER TABLE organization_ai_settings DROP COLUMN base_url`)
	if err := a.migrateAI(); err != nil {
		t.Fatal(err)
	}
	s, err := a.readAISettings(context.Background(), a.db)
	if err != nil || s.BaseURL != aiDefaultBaseURL || !bytes.Equal(s.Encrypted, encrypted) || !s.Enabled {
		t.Fatal("legacy migration changed encrypted credentials or defaults", err)
	}
}

func TestAIEndpointAllFeaturesUseReservedAddressAndRejectOldResults(t *testing.T) {
	for _, feature := range []string{"title", "refine", "cases", "review"} {
		for _, mutate := range []bool{false, true} {
			t.Run(fmt.Sprint(feature, "/configChange=", mutate), func(t *testing.T) {
				a := aiApp(t)
				setAIGateway(t, a, "https://api.owlai.tech")
				x, item := aiReviewCase(t, a)
				path, body, table, want, payload := titlePath, titleBody(nil), "ai_title_requests", 200, jsonText(map[string]any{"title": generatedTitle, "insufficient": false})
				switch feature {
				case "refine":
					path = "/api/ai/requirement-refine"
					payload = refinementSample
				case "cases":
					path = fmt.Sprintf("/api/requirements/%d/ai-test-cases", x.ID)
					body = jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt})
					table = "ai_test_case_drafts"
					want = 201
					payload = aiExampleJSON()
				case "review":
					path = fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID)
					body = jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"})
					table = "ai_test_case_reviews"
					payload = aiReviewJSON()
				}
				capability := apiRequest(a, "GET", path, "u_admin", projectID, "")
				if capability.Code != 200 || jsonMap(t, capability)["baseUrl"] != "https://api.owlai.tech/v1" || strings.Contains(capability.Body.String(), aiTestSecret) {
					t.Fatal("capability omitted public endpoint or exposed secret")
				}
				if mutate {
					// The reservation commits after reading one credential/address
					// snapshot. Simulate a concurrent change at that exact boundary.
					bulkFixtureExec(t, a, `CREATE TRIGGER change_ai_destination AFTER INSERT ON `+table+` BEGIN UPDATE organization_ai_settings SET base_url='https://other.example.com/v1',version=version+1; END`)
					want = 409
				}
				calls := 0
				aiMock(a, func(request *http.Request) (*http.Response, error) {
					calls++
					if request.URL.String() != "https://api.owlai.tech/v1/responses" || request.Method != "POST" || request.Header.Get("Authorization") != "Bearer "+aiTestSecret {
						t.Fatal("reserved key/address pair mixed with newly read configuration")
					}
					var data map[string]any
					if err := json.NewDecoder(request.Body).Decode(&data); err != nil {
						t.Fatal(err)
					}
					if data["store"] != false || data["tools"] != nil {
						t.Fatal("Responses safety payload changed")
					}
					return aiResponse(payload), nil
				})
				w := apiRequest(a, "POST", path, "u_admin", projectID, body)
				if w.Code != want || calls != 1 || strings.Contains(w.Body.String(), aiTestSecret) {
					t.Fatal("generation result/version policy", w.Code, w.Body.String(), calls)
				}
			})
		}
	}
}

func TestAIEndpointProviderErrorsAndRedirectsNeverLeakKeys(t *testing.T) {
	a := aiApp(t)
	for _, mode := range []string{"error", "redirect"} {
		calls := 0
		aiMock(a, func(r *http.Request) (*http.Response, error) {
			calls++
			if mode == "error" {
				return nil, errors.New(aiTestSecret + " provider internal secret")
			}
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://other.example.com/steal"}}, Body: io.NopCloser(strings.NewReader(aiTestSecret))}, nil
		})
		w := apiRequest(a, "POST", titlePath, "u_admin", projectID, titleBody(nil))
		if w.Code != 502 || calls != 1 || strings.Contains(w.Body.String(), aiTestSecret) || strings.Contains(w.Body.String(), "provider internal secret") {
			t.Fatal("unsafe provider failure", w.Code, w.Body.String())
		}
	}
}

func TestAIEndpointRejectsValidSuccessPayloadsReflectingKey(t *testing.T) {
	a := aiApp(t)
	for _, feature := range []string{"title", "refine", "cases", "review"} {
		var payload string
		switch feature {
		case "title":
			payload = jsonText(map[string]any{"title": aiTestSecret, "insufficient": false})
		case "refine":
			payload = strings.Replace(refinementSample, "已有背景", aiTestSecret, 1)
		case "cases":
			payload = strings.Replace(aiExampleJSON(), "正常提交", aiTestSecret, 1)
		case "review":
			payload = strings.Replace(aiReviewJSON(), "用例覆盖主流程，但需补充异常分支。", aiTestSecret, 1)
		}
		// Escaped JSON must not evade the secret check.
		payload = strings.ReplaceAll(payload, "sk-test", `\u0073k-test`)
		aiMock(a, func(*http.Request) (*http.Response, error) { return aiResponse(payload), nil })
		var err error
		switch feature {
		case "title", "refine":
			_, err = a.generateAIRequirementText(context.Background(), aiTestSecret, aiDefaultModel, "https://api.owlai.tech/v1", titleDescription, feature == "refine")
		case "cases":
			_, err = a.generateAITestCasesWithOptions(context.Background(), aiTestSecret, aiDefaultModel, "https://api.owlai.tech/v1", Requirement{Title: "Test"}, defaultAITestGenerationOptions())
		case "review":
			_, err = a.reviewAITestCaseAt(context.Background(), aiTestSecret, aiDefaultModel, "https://api.owlai.tech/v1", aiTestCaseReviewInput{Case: TestCase{Title: "Test"}, Mode: "standard"})
		}
		if err == nil || strings.Contains(err.Error(), aiTestSecret) {
			t.Fatal("key reflected in valid success output", feature)
		}
	}
}

func TestAIEndpointRechecksSessionAfterSettingsWriteLock(t *testing.T) {
	a := aiApp(t)
	before, err := a.readAISettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	bulkFixtureExec(t, a, `CREATE TRIGGER revoke_ai_admin_at_write_lock AFTER UPDATE ON organization_write_locks BEGIN UPDATE auth_sessions SET revoked_at='2026-01-01T00:00:00Z' WHERE user_id='u_admin'; END`)
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"baseUrl": "https://api.owlai.tech", "reuseKey": true, "expectedVersion": before.Version}))
	if w.Code != 401 {
		t.Fatal("revoked session altered credential destination", w.Code, w.Body.String())
	}
	after, err := a.readAISettings(context.Background(), a.db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed session check partially changed AI settings", err)
	}
}

func TestAIEndpointNewKeyBindingRejectsStaleFormsInBothDirections(t *testing.T) {
	for _, customFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("customFirst=", customFirst), func(t *testing.T) {
			a := aiApp(t)
			if customFirst {
				setAIGateway(t, a, "https://api.owlai.tech")
			}
			old, err := a.readAISettings(context.Background(), a.db)
			if err != nil {
				t.Fatal(err)
			}
			next := aiDefaultBaseURL
			if !customFirst {
				next = "https://api.owlai.tech"
			}
			setAIGateway(t, a, next)
			current, err := a.readAISettings(context.Background(), a.db)
			if err != nil {
				t.Fatal(err)
			}
			for _, payload := range []map[string]any{
				{"apiKey": "another-synthetic-key-from-stale-page-12345"},
				{"apiKey": "another-synthetic-key-from-stale-page-12345", "baseUrl": old.BaseURL},
				{"apiKey": "another-synthetic-key-from-stale-page-12345", "baseUrl": old.BaseURL, "expectedVersion": old.Version},
			} {
				w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(payload))
				if w.Code != 409 {
					t.Fatal("stale key replacement accepted", customFirst, w.Code)
				}
				after, err := a.readAISettings(context.Background(), a.db)
				if err != nil || !reflect.DeepEqual(after, current) {
					t.Fatal("stale form changed active credential/address pair")
				}
			}
		})
	}
}

func TestAIEndpointSavesAddressBeforeAnyKeyWithoutCallingProvider(t *testing.T) {
	a := testApp(t)
	aiMock(a, func(*http.Request) (*http.Response, error) {
		t.Fatal("configuration caused a model call")
		return nil, nil
	})
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, `{"baseUrl":"https://api.owlai.tech","enabled":false,"expectedVersion":0}`)
	if w.Code != 200 || jsonMap(t, w)["configured"] != false || jsonMap(t, w)["baseUrl"] != "https://api.owlai.tech/v1" {
		t.Fatal("address-only initialization failed", w.Code, w.Body.String())
	}
}

func TestAIEndpointRevokedGenerationSessionsCannotMakePaidCalls(t *testing.T) {
	for _, feature := range []string{"title", "refine", "cases", "review"} {
		t.Run(feature, func(t *testing.T) {
			a := aiApp(t)
			x, item := aiReviewCase(t, a)
			path, body := titlePath, titleBody(nil)
			switch feature {
			case "refine":
				path = "/api/ai/requirement-refine"
			case "cases":
				path = fmt.Sprintf("/api/requirements/%d/ai-test-cases", x.ID)
				body = jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt})
			case "review":
				path = fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID)
				body = jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"})
			}
			aiMock(a, func(*http.Request) (*http.Response, error) {
				t.Fatal("revoked session reached provider", feature)
				return nil, nil
			})
			bulkFixtureExec(t, a, `CREATE TRIGGER revoke_ai_generation_at_lock AFTER UPDATE ON organization_write_locks BEGIN UPDATE auth_sessions SET revoked_at='2026-01-01T00:00:00Z' WHERE user_id='u_admin'; END`)
			w := apiRequest(a, "POST", path, "u_admin", projectID, body)
			if w.Code != 401 {
				t.Fatal("revoked generation accepted", feature, w.Code, w.Body.String())
			}
		})
	}
}
