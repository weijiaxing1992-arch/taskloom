package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	auditHistoryDefaultPageSize = 25
	auditHistoryMaxPageSize     = 100
	auditHistoryMaxChanges      = 20
	auditHistoryValueLimit      = 240
)

// auditHistoryCursor 只保存已展示记录的稳定时间与行标识。游标即使被伪造也不会
// 放宽租户/项目 WHERE 条件，只会让当前已授权范围内的翻页位置发生变化。
type auditHistoryCursor struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"t"`
	ID        int64  `json:"i"`
}

type auditHistoryQuery struct {
	ObjectType string
	Action     string
	ActorID    string
	From       string
	To         string
	Limit      int
	Cursor     *auditHistoryCursor
}

type auditHistoryChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type auditHistoryItem struct {
	ID             int64                `json:"id"`
	ActorID        string               `json:"actorId"`
	ActorName      string               `json:"actorName"`
	ObjectType     string               `json:"objectType"`
	ObjectID       string               `json:"objectId"`
	Action         string               `json:"action"`
	CreatedAt      string               `json:"createdAt"`
	Changes        []auditHistoryChange `json:"changes"`
	ChangesTrimmed bool                 `json:"changesTrimmed"`
}

// migrateAuditHistory 仅补充读取索引，不会更新、归档或清理既有审计记录。
// 审计表保持只追加：历史的 before/after 是取证依据，绝不能因展示功能被重写。
func (a *App) migrateAuditHistory() error {
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_scope_timeline ON audit_logs(tenant_id,project_id,created_at DESC)`,
		// 审计时间来自多个历史写入版本：有的精确到秒，有的带小数秒。
		// 直接按 TEXT 排序会把“12:00:00Z”排在“12:00:00.250Z”之前，
		// 也会在按日期筛选时漏掉当天零点后的带小数秒记录。表达式索引与
		// 查询中的 julianday(created_at) 保持一致，既修正顺序又避免全表排序。
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_scope_timeline_julian ON audit_logs(tenant_id,project_id,julianday(created_at) DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_scope_actor_timeline ON audit_logs(tenant_id,project_id,actor_id,created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_scope_object_timeline ON audit_logs(tenant_id,project_id,object_type,created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_scope_action_timeline ON audit_logs(tenant_id,project_id,action,created_at DESC)`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// auditHistory 是项目管理员的只读审计入口。scopedAPI 已先验证当前用户属于
// 请求项目；这里仍重复校验管理员能力，避免以后路由复用或直接调用时误泄漏历史。
func (a *App) auditHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持读取审计记录")
		return
	}
	if !a.canManageProject(a.uid(), a.pid()) {
		fail(w, http.StatusForbidden, "audit_access_denied", "仅项目管理员可以查看操作审计")
		return
	}
	query, err := parseAuditHistoryQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_audit_query", err.Error())
		return
	}

	where := []string{"al.tenant_id=?", "al.project_id=?"}
	args := []any{tenantID, a.pid()}
	if query.ObjectType != "" {
		where, args = append(where, "al.object_type=?"), append(args, query.ObjectType)
	}
	if query.Action != "" {
		where, args = append(where, "al.action=?"), append(args, query.Action)
	}
	if query.ActorID != "" {
		where, args = append(where, "al.actor_id=?"), append(args, query.ActorID)
	}
	// created_at 在早期版本使用 RFC3339，较新的路径会使用 RFC3339Nano。
	// SQLite 的文本比较不能正确比较这两种字符串（例如 .250Z 与 Z），
	// 因此所有时间范围、排序和游标都使用同一个时间轴表达式。
	const timeline = "julianday(al.created_at)"
	if query.From != "" {
		where, args = append(where, timeline+">=julianday(?)"), append(args, query.From)
	}
	if query.To != "" {
		where, args = append(where, timeline+"<julianday(?)"), append(args, query.To)
	}
	if query.Cursor != nil {
		// 时间相同的审计事件以递减 ID 打破平局；避免翻页时重复或跳过同秒事件。
		// 游标也走时间轴表达式，保证跨 RFC3339/RFC3339Nano 的翻页稳定。
		where, args = append(where, "("+timeline+"<julianday(?) OR ("+timeline+"=julianday(?) AND al.rowid<?))"), append(args, query.Cursor.CreatedAt, query.Cursor.CreatedAt, query.Cursor.ID)
	}
	args = append(args, query.Limit+1)
	// before_json/after_json 是审计表首次上线后补充的列。少量历史部署中存在
	// NULL 值，因此读取时兜底为空对象；不能因一条旧记录让整页变更历史报错。
	statement := `SELECT al.rowid,al.actor_id,COALESCE(u.name,''),al.object_type,al.object_id,al.action,COALESCE(al.before_json,'{}'),COALESCE(al.after_json,'{}'),al.created_at
		FROM audit_logs al
		LEFT JOIN users u ON u.tenant_id=al.tenant_id AND u.id=al.actor_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + timeline + ` DESC,al.rowid DESC
		LIMIT ?`
	rows, err := a.db.QueryContext(r.Context(), statement, args...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "审计记录暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := make([]auditHistoryItem, 0, query.Limit)
	hasMore := false
	for rows.Next() {
		var item auditHistoryItem
		var beforeJSON, afterJSON string
		if err = rows.Scan(&item.ID, &item.ActorID, &item.ActorName, &item.ObjectType, &item.ObjectID, &item.Action, &beforeJSON, &afterJSON, &item.CreatedAt); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "审计记录暂时无法读取，请稍后重试")
			return
		}
		if len(items) == query.Limit {
			hasMore = true
			break
		}
		item.Changes, item.ChangesTrimmed = auditHistoryChanges(beforeJSON, afterJSON)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "审计记录暂时无法读取，请稍后重试")
		return
	}
	nextCursor := ""
	if hasMore {
		last := items[len(items)-1]
		nextCursor = encodeAuditHistoryCursor(auditHistoryCursor{Version: 1, CreatedAt: last.CreatedAt, ID: last.ID})
	}
	write(w, http.StatusOK, map[string]any{
		"items":      items,
		"pageSize":   query.Limit,
		"hasMore":    hasMore,
		"nextCursor": nextCursor,
	})
}

func parseAuditHistoryQuery(r *http.Request) (auditHistoryQuery, error) {
	values := r.URL.Query()
	query := auditHistoryQuery{Limit: auditHistoryDefaultPageSize}
	var err error
	for _, field := range []struct {
		key  string
		dest *string
	}{{"objectType", &query.ObjectType}, {"action", &query.Action}, {"actorId", &query.ActorID}} {
		if *field.dest, err = validAuditFilter(values.Get(field.key)); err != nil {
			return query, err
		}
	}
	if values.Has("limit") {
		query.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || query.Limit < 1 || query.Limit > auditHistoryMaxPageSize {
			return query, errors.New("分页大小无效")
		}
	}
	if raw := values.Get("from"); raw != "" {
		query.From, err = parseAuditBound(raw, false)
		if err != nil {
			return query, err
		}
	}
	if raw := values.Get("to"); raw != "" {
		query.To, err = parseAuditBound(raw, true)
		if err != nil {
			return query, err
		}
	}
	if query.From != "" && query.To != "" && query.From >= query.To {
		return query, errors.New("结束时间必须晚于开始时间")
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeAuditHistoryCursor(raw)
		if err != nil {
			return query, errors.New("分页游标无效")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func validAuditFilter(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if utf8.RuneCountInString(value) > 100 {
		return "", errors.New("筛选条件过长")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", errors.New("筛选条件格式无效")
		}
	}
	return value, nil
}

// parseAuditBound 统一将日期转换为 UTC 半开区间。日期型结束值指向下一天零点，
// 这样筛选“至 9 月 4 日”不会意外遗漏当天晚些时候发生的审计事件。
func parseAuditBound(raw string, upper bool) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	// 原生日期控件会返回 YYYY-MM-DD；部分内嵌浏览器和从日历复制的旧链接会
	// 使用斜杠。两种无歧义格式统一转换为 UTC，确保两端共用半开区间语义。
	for _, layout := range []string{"2006-01-02", "2006/01/02"} {
		day, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if upper {
			day = day.AddDate(0, 0, 1)
		}
		return day.UTC().Format(time.RFC3339Nano), nil
	}
	stamp, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", errors.New("时间格式无效")
	}
	if upper {
		stamp = stamp.Add(time.Nanosecond)
	}
	return stamp.UTC().Format(time.RFC3339Nano), nil
}

func encodeAuditHistoryCursor(cursor auditHistoryCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeAuditHistoryCursor(raw string) (auditHistoryCursor, error) {
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(value) == 0 || len(value) > 512 {
		return auditHistoryCursor{}, errors.New("invalid cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor auditHistoryCursor
	if err = decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.ID < 1 || cursor.CreatedAt == "" {
		return auditHistoryCursor{}, errors.New("invalid cursor")
	}
	if _, err = time.Parse(time.RFC3339, cursor.CreatedAt); err != nil {
		return auditHistoryCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func auditHistoryChanges(beforeRaw, afterRaw string) ([]auditHistoryChange, bool) {
	before := redactAuditJSON(beforeRaw)
	after := redactAuditJSON(afterRaw)
	changes := make([]auditHistoryChange, 0, 4)
	trimmed := appendAuditChanges("", before, after, &changes, 0)
	if len(changes) == 0 {
		changes = append(changes, auditHistoryChange{Field: "审计快照", Before: auditValueText(before), After: auditValueText(after)})
	}
	return changes, trimmed
}

// redactAuditJSON 在任何序列化或差异计算之前执行；即使调用方是管理员也不能
// 从审计展示接口取回密码、token、密钥或 Webhook。非 JSON 内容同样不透传，
// 避免旧版本意外把敏感文本写入审计列后被列表页二次暴露。
func redactAuditJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return map[string]any{"审计数据": "已隐藏非结构化审计数据"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return map[string]any{"审计数据": "已隐藏非结构化审计数据"}
	}
	// before/after 正常应是对象或数组。标量 JSON 没有字段语义，无法可靠判断
	// 其中是否夹带旧系统写入的口令，因此按非结构化内容处理而不是原样展示。
	switch value.(type) {
	case map[string]any, []any:
		return redactAuditValue(value)
	default:
		return map[string]any{"审计数据": "已隐藏非结构化审计数据"}
	}
}

func redactAuditValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, nested := range current {
			if sensitiveAuditKey(key) {
				// 使用统一展示名，不暴露密钥字段名称及其值。
				result["敏感字段"] = "已脱敏"
				continue
			}
			result[key] = redactAuditValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, nested := range current {
			result[index] = redactAuditValue(nested)
		}
		return result
	default:
		return value
	}
}

func sensitiveAuditKey(key string) bool {
	value := strings.ToLower(strings.TrimSpace(key))
	// 审计写入既有英文 API 字段，也有人工维护时留下的中文键名；两类都必须
	// 在展示层统一脱敏，不能因为键名语言不同而回显凭证。
	for _, marker := range []string{
		"password", "key", "token", "secret", "webhook", "credential",
		"密码", "口令", "密钥", "秘钥", "令牌", "凭证", "授权码", "私钥", "公钥", "回调", "钩子",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func appendAuditChanges(path string, before, after any, changes *[]auditHistoryChange, depth int) bool {
	if reflect.DeepEqual(before, after) {
		return false
	}
	if len(*changes) >= auditHistoryMaxChanges {
		return true
	}
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap && depth < 4 {
		keys := make([]string, 0, len(beforeMap)+len(afterMap))
		seen := map[string]bool{}
		for key := range beforeMap {
			seen[key] = true
			keys = append(keys, key)
		}
		for key := range afterMap {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		trimmed := false
		for _, key := range keys {
			field := key
			if path != "" {
				field = path + "." + key
			}
			if appendAuditChanges(field, beforeMap[key], afterMap[key], changes, depth+1) {
				trimmed = true
				break
			}
		}
		return trimmed
	}
	if path == "" {
		path = "审计快照"
	}
	*changes = append(*changes, auditHistoryChange{Field: path, Before: auditValueText(before), After: auditValueText(after)})
	return false
}

func auditValueText(value any) string {
	if value == nil {
		return "—"
	}
	if text, ok := value.(string); ok {
		return auditTrimText(text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "已隐藏不可展示值"
	}
	return auditTrimText(string(encoded))
}

func auditTrimText(value string) string {
	text := strings.TrimSpace(value)
	if utf8.RuneCountInString(text) <= auditHistoryValueLimit {
		return text
	}
	return string([]rune(text)[:auditHistoryValueLimit]) + "…"
}
