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

// 外发目的地固定，模型限定为本项目已验证的配置；不提供任意 baseURL，也不自动升级模型。
const aiEndpoint = "https://api.openai.com/v1/responses"

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
	Model     string
	Encrypted []byte
	Enabled   bool
	Version   int
}

func (a *App) migrateAI() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS organization_ai_settings(tenant_id TEXT PRIMARY KEY,encrypted_key BLOB NOT NULL,model TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS ai_test_case_drafts(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,user_id TEXT NOT NULL,requirement_updated_at TEXT NOT NULL,source_hash TEXT NOT NULL,settings_version INTEGER NOT NULL,model TEXT NOT NULL,status TEXT NOT NULL,cases_json TEXT NOT NULL DEFAULT '[]',selected_json TEXT NOT NULL DEFAULT '[]',imported_json TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL,expires_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_ai_requests_rate ON ai_test_case_drafts(tenant_id,user_id,created_at);`)
	if err != nil {
		return err
	}
	return a.migrateAITitles()
}

func (a *App) readAISettings(ctx context.Context, q stateStore) (aiSettings, error) {
	s := aiSettings{Model: aiDefaultModel}
	err := q.QueryRowContext(ctx, `SELECT encrypted_key,model,enabled,version FROM organization_ai_settings WHERE tenant_id=?`, tenantID).Scan(&s.Encrypted, &s.Model, &s.Enabled, &s.Version)
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
	return map[string]any{"provider": "openai", "configured": len(s.Encrypted) > 0, "enabled": s.Enabled, "model": s.Model, "models": aiModels, "canManage": true}
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
		APIKey  *string `json:"apiKey"`
		Model   *string `json:"model"`
		Enabled *bool   `json:"enabled"`
		Clear   bool    `json:"clear"`
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
	} else {
		if input.APIKey != nil {
			value := strings.TrimSpace(*input.APIKey)
			if len(value) < 20 || len(value) > 512 || !strings.HasPrefix(value, "sk-") || strings.ContainsAny(value, " \t\r\n\x00") {
				failAI(w, orgInvalid("API Key 格式不正确"))
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
	}
	if s.Enabled && len(s.Encrypted) == 0 {
		failAI(w, aiFailure(409, "ai_not_configured", "请先配置 OpenAI API Key"))
		return
	}
	// 任意配置变更递增版本，生成完成后由上层复查；旧配置下进行中的结果不能继续导入。
	s.Version++
	_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_ai_settings(tenant_id,encrypted_key,model,enabled,version,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id)DO UPDATE SET encrypted_key=excluded.encrypted_key,model=excluded.model,enabled=excluded.enabled,version=excluded.version,updated_at=excluded.updated_at`, tenantID, s.Encrypted, s.Model, s.Enabled, s.Version, orgNow())
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
