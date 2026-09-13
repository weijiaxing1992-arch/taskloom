package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
)

// 官方模型目录核对于 2026-09-08；展示支持不等于当前 API 账号已获授权。
var aiModels = []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-cyber", "gpt-5-mini", "gpt-5-mini-2025-08-07"}

// 扩展模型列表不自动提升现有或未配置企业的费用档位。
const aiDefaultModel = "gpt-5-mini"

func aiModelReasoning(model string) map[string]string {
	// Cyber 文档未公布 effort 枚举，不把通用模型的参数强加给受限专用接口。
	if model == "gpt-5.6-cyber" {
		return nil
	}
	return map[string]string{"effort": "low"}
}

type aiSettings struct {
	Model            string
	BaseURL          string
	Encrypted        []byte
	Enabled          bool
	Version          int
	AutoReleaseNotes bool
}

func (a *App) migrateAI() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS organization_ai_settings(tenant_id TEXT PRIMARY KEY,encrypted_key BLOB NOT NULL,model TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ai_test_case_drafts(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,user_id TEXT NOT NULL,requirement_updated_at TEXT NOT NULL,source_hash TEXT NOT NULL,settings_version INTEGER NOT NULL,model TEXT NOT NULL,status TEXT NOT NULL,cases_json TEXT NOT NULL DEFAULT '[]',selected_json TEXT NOT NULL DEFAULT '[]',imported_json TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_ai_requests_rate ON ai_test_case_drafts(tenant_id,user_id,created_at);`)
	if err != nil {
		return err
	}
	var hasBaseURL int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('organization_ai_settings') WHERE name='base_url'`).Scan(&hasBaseURL); err != nil {
		return err
	}
	if hasBaseURL == 0 {
		if _, err = a.db.Exec(`ALTER TABLE organization_ai_settings ADD COLUMN base_url TEXT NOT NULL DEFAULT 'https://api.openai.com/v1'`); err != nil {
			return err
		}
	}
	// 旧企业默认不自动外发需求；启用前由管理员单独确认用途和服务地址。
	var hasAutoReleaseNotes int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('organization_ai_settings') WHERE name='auto_release_notes'`).Scan(&hasAutoReleaseNotes); err != nil {
		return err
	}
	if hasAutoReleaseNotes == 0 {
		if _, err = a.db.Exec(`ALTER TABLE organization_ai_settings ADD COLUMN auto_release_notes INTEGER NOT NULL DEFAULT 0 CHECK(auto_release_notes IN (0,1))`); err != nil {
			return err
		}
	}
	return a.migrateAITitles()
}

func (a *App) readAISettings(ctx context.Context, q stateStore) (aiSettings, error) {
	s := aiSettings{Model: aiDefaultModel, BaseURL: aiDefaultBaseURL, Encrypted: []byte{}}
	err := q.QueryRowContext(ctx, `SELECT encrypted_key,model,enabled,version,COALESCE(NULLIF(base_url,''),'https://api.openai.com/v1'),auto_release_notes FROM organization_ai_settings WHERE tenant_id=?`, tenantID).Scan(&s.Encrypted, &s.Model, &s.Enabled, &s.Version, &s.BaseURL, &s.AutoReleaseNotes)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// 仅真实企业管理员可配置有付费能力的 Key；项目管理员、委派管理权限和代访问均不能代替此检查。
func (a *App) requireAIAdmin(ctx context.Context, q stateStore) error {
	if a.impersonation != nil {
		return &organizationError{403, "impersonation_restricted", "代访问期间不能配置 AI 服务"}
	}
	_, admin, err := a.organizationAccess(ctx, q)
	if err != nil {
		return err
	}
	if !admin {
		return &organizationError{403, "forbidden", "仅企业管理员可配置 AI 服务"}
	}
	return nil
}

// 复用持久主密钥，但 AAD 与 Webhook 隔离并绑定企业；恢复数据库时必须保留原主密钥文件。
func sealAIKey(key []byte, plain string) ([]byte, error) {
	c, err := webhookCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.Seal(nonce, nonce, []byte(plain), []byte("devflow:openai-api-key:v1:"+tenantID)), nil
}
func openAIKey(key, encrypted []byte) (string, error) {
	c, err := webhookCipher(key)
	if err != nil || len(encrypted) < 12 {
		return "", errors.New("AI encryption unavailable")
	}
	plain, err := c.Open(nil, encrypted[:c.NonceSize()], encrypted[c.NonceSize():], []byte("devflow:openai-api-key:v1:"+tenantID))
	if err != nil {
		return "", errors.New("AI encryption unavailable")
	}
	return string(plain), nil
}

func failAI(w http.ResponseWriter, err error) {
	var state stateError
	if errors.As(err, &state) {
		fail(w, state.status, state.code, state.message)
		return
	}
	var specific *organizationError
	if errors.As(err, &specific) {
		fail(w, specific.Status, specific.Code, specific.Message)
		return
	}
	if failCustomFieldValidation(w, err) {
		return
	}
	fail(w, 503, "ai_unavailable", "AI 服务暂时不可用，请稍后重试")
}
func aiFailure(status int, code, message string) error {
	return &organizationError{status, code, message}
}
func aiSettingsResponse(s aiSettings) map[string]any {
	base := s.BaseURL
	if base == "" {
		base = aiDefaultBaseURL
	}
	mode := "custom"
	if base == aiDefaultBaseURL {
		mode = "default"
	}
	return map[string]any{"provider": "openai", "configured": len(s.Encrypted) > 0, "enabled": s.Enabled, "model": s.Model, "models": aiModels, "canManage": true, "baseUrl": base, "endpointMode": mode, "version": s.Version, "autoReleaseNotes": s.AutoReleaseNotes}
}

func (a *App) organizationAISettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "PATCH" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if err := a.requireAIAdmin(r.Context(), a.db); err != nil {
		failAI(w, err)
		return
	}
	if r.Method == "GET" {
		s, err := a.readAISettings(r.Context(), a.db)
		if err != nil {
			failAI(w, err)
			return
		}
		write(w, 200, aiSettingsResponse(s))
		return
	}
	var input struct {
		APIKey           *string `json:"apiKey"`
		Model            *string `json:"model"`
		Enabled          *bool   `json:"enabled"`
		Clear            bool    `json:"clear"`
		BaseURL          *string `json:"baseUrl"`
		ReuseKey         bool    `json:"reuseKey"`
		ExpectedVersion  *int    `json:"expectedVersion"`
		AutoReleaseNotes *bool   `json:"autoReleaseNotes"`
		ConfirmAutomatic bool    `json:"confirmAutomatic"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	if input.Clear && input.APIKey != nil {
		failAI(w, orgInvalid("清除与设置 API Key 不能同时提交"))
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err == nil {
		err = a.requireAdministrationSession(r.Context(), tx)
	}
	if err == nil {
		err = a.requireAIAdmin(r.Context(), tx)
	}
	if err != nil {
		failAI(w, err)
		return
	}
	s, err := a.readAISettings(r.Context(), tx)
	if err != nil {
		failAI(w, err)
		return
	}
	before := aiSettingsResponse(s)
	previousAuto, previousBaseURL := s.AutoReleaseNotes, s.BaseURL
	if input.ExpectedVersion != nil && (*input.ExpectedVersion < 0 || *input.ExpectedVersion != s.Version) {
		failAI(w, aiFailure(409, "ai_settings_changed", "AI 配置已被其他管理员修改，请刷新后重新确认"))
		return
	}
	if input.BaseURL != nil {
		base, baseErr := normalizeAIBaseURL(*input.BaseURL)
		if baseErr != nil {
			failAI(w, baseErr)
			return
		}
		if base != s.BaseURL && len(s.Encrypted) > 0 && input.APIKey == nil && !input.Clear && !input.ReuseKey {
			failAI(w, aiFailure(409, "ai_key_reuse_confirmation_required", "更换 API 地址并保留现有密钥前，请明确确认将密钥发送到新地址"))
			return
		}
		if base != s.BaseURL && len(s.Encrypted) > 0 && input.APIKey == nil && !input.Clear && input.ExpectedVersion == nil {
			failAI(w, aiFailure(409, "ai_settings_changed", "AI 配置已被其他管理员修改，请刷新后重新确认"))
			return
		}
		s.BaseURL = base
	}
	if input.Model != nil {
		if !validChoice(*input.Model, aiModels) {
			failAI(w, orgInvalid("AI 模型不受支持"))
			return
		}
		s.Model = *input.Model
	}
	if input.Clear {
		s.Encrypted = []byte{}
		s.Enabled = false
		s.AutoReleaseNotes = false
	} else {
		if input.APIKey != nil {
			value := strings.TrimSpace(*input.APIKey)
			valid := len(value) >= 20 && len(value) <= 512
			for _, c := range value {
				if c < 0x21 || c > 0x7e {
					valid = false
				}
			}
			if !valid {
				failAI(w, orgInvalid("API Key 格式不正确"))
				return
			}
			// A replacement key is bound to the configuration the administrator
			// actually saw. This applies in both directions: an old custom-key
			// form must not silently send that key to the official endpoint either.
			if s.Version > 0 && input.ExpectedVersion == nil {
				failAI(w, aiFailure(409, "ai_settings_changed", "AI 配置已被其他管理员修改，请刷新后重新确认"))
				return
			}
			s.Encrypted, err = sealAIKey(a.wecomKey, value)
			if err != nil {
				failAI(w, err)
				return
			}
		}
		if input.Enabled != nil {
			s.Enabled = *input.Enabled
		}
		if input.AutoReleaseNotes != nil {
			s.AutoReleaseNotes = *input.AutoReleaseNotes
		}
	}
	if s.Enabled && len(s.Encrypted) == 0 {
		failAI(w, aiFailure(409, "ai_not_configured", "请先配置 OpenAI API Key"))
		return
	}
	if s.AutoReleaseNotes && (!previousAuto || s.BaseURL != previousBaseURL) {
		if input.ExpectedVersion == nil {
			failAI(w, aiFailure(409, "ai_settings_changed", "请刷新配置后确认自动生成升级日志"))
			return
		}
		if !input.ConfirmAutomatic {
			failAI(w, aiFailure(409, "ai_release_consent_required", "请确认完成迭代后自动向所配置服务发送已完成需求内容，生成升级日志可能产生模型费用"))
			return
		}
		if !s.Enabled || len(s.Encrypted) == 0 {
			failAI(w, aiFailure(409, "ai_not_configured", "请先配置并启用 AI 服务，再开启自动升级日志"))
			return
		}
	}
	// 任意配置变更递增版本，生成完成后由上层复查；旧配置下进行中的结果不能继续导入。
	s.Version++
	_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_ai_settings(tenant_id,encrypted_key,model,enabled,version,updated_at,base_url,auto_release_notes)VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id)DO UPDATE SET encrypted_key=excluded.encrypted_key,model=excluded.model,enabled=excluded.enabled,version=excluded.version,updated_at=excluded.updated_at,base_url=excluded.base_url,auto_release_notes=excluded.auto_release_notes`, tenantID, s.Encrypted, s.Model, s.Enabled, s.Version, orgNow(), s.BaseURL, s.AutoReleaseNotes)
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "ai_settings", tenantID, "updated", before, aiSettingsResponse(s))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	write(w, 200, aiSettingsResponse(s))
}
