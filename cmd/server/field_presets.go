package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type fieldPreset struct {
	FieldDefinition
	Installed  bool  `json:"installed"`
	ExistingID int64 `json:"existingId,omitempty"`
}
type systemField struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Enabled     bool     `json:"enabled"`
	System      bool     `json:"system"`
	MemberRoles []string `json:"memberRoles"`
}

func requirementSystemFields() []systemField {
	fields := []systemField{}
	for _, entry := range [][3]string{
		{"title", "标题", "text"}, {"type", "需求类型", "single_select"}, {"description", "详细描述", "textarea"}, {"acceptance", "验收标准", "textarea"},
		{"status", "状态", "single_select"}, {"priority", "优先级", "single_select"}, {"category", "分类", "single_select"}, {"sprint", "迭代", "single_select"},
		{"parentId", "父需求", "number"}, {"startDate", "预计开始", "date"}, {"endDate", "预计结束", "date"}, {"estimatedHours", "预计工时", "number"}, {"actualHours", "实际工时", "number"},
		{"assigneeUserIds", "处理人 / 开发人员", "users"}, {"ownerUserIds", "产品经理", "users"}, {"discipline", "职能", "single_select"}, {"progress", "进度", "number"},
		{"role.frontend.userIds", "前端工程师", "users"}, {"role.frontend.value", "前端开发难度", "number"}, {"role.backend.userIds", "后端工程师", "users"}, {"role.backend.value", "后端难度", "number"},
		{"role.algorithm.userIds", "算法及架构师", "users"}, {"role.algorithm.value", "算法难度", "number"}, {"role.ui.userIds", "UI 设计师", "users"}, {"role.ui.value", "UI 难度", "number"},
		{"role.product.userIds", "产品负责人", "users"}, {"role.product.value", "产品难度", "number"}, {"weightTotal", "总权重", "number"},
		{"tags", "标签", "multi_select"}, {"remarks", "备注", "textarea"}, {"sensitive", "涉及敏感数据", "boolean"}, {"authImpact", "涉及权限认证", "boolean"}, {"createdAt", "创建时间", "date"}, {"updatedAt", "更新时间", "date"},
	} {
		fields = append(fields, systemField{Key: entry[0], Name: entry[1], Type: entry[2], Required: entry[0] == "title", Enabled: true, System: true, MemberRoles: memberRolesForKey(entry[0])})
	}
	return fields
}
func requirementFieldPresets() []FieldDefinition {
	var fields []FieldDefinition
	for i, entry := range [][3]string{{"frontend_leads", "前端组长", "users"}, {"backend_leads", "后端组长", "users"}, {"managers", "管理人员", "users"}, {"testers", "测试人员", "users"}, {"operations_engineers", "运维工程师", "users"}, {"requirement_group", "需求分组", "single_select"}, {"operations_difficulty", "运维开发难度", "number"}, {"extra_weight", "额外权重", "number"}, {"release_version", "版本", "text"}} {
		d := FieldDefinition{ObjectType: "requirement", Key: entry[0], Name: entry[1], Type: entry[2], Enabled: true, Searchable: entry[2] == "text", Filterable: true, ListVisible: true, SortOrder: 100 + (i+1)*10}
		d.MemberRoles = fieldMemberRoles(d)
		if d.Type == "single_select" {
			d.Options = []string{"产品功能", "体验优化", "技术优化", "客户定制", "合规安全"}
		}
		if d.Type == "users" {
			d.Description = "可绑定多位项目成员；可按真实部门限定候选范围"
		}
		if d.Key == "extra_weight" || d.Key == "operations_difficulty" {
			d.Description = "独立数值字段，不自动计入五类职能难度总权重"
		}
		fields = append(fields, d)
	}
	return fields
}
func presetObject(value string) bool { return value == "requirement" }
func (a *App) fieldPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	object := r.URL.Query().Get("objectType")
	if object == "" {
		object = "requirement"
	}
	if !validChoice(object, []string{"requirement", "defect", "test_case", "sprint"}) {
		fail(w, 422, "validation_error", "当前对象暂无默认字段预设")
		return
	}
	defs, err := a.definitions(object, false)
	if err != nil {
		fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	// A deleted preset remains intentionally installed. Never advertise it as
	// missing, or the one-click action would continually offer to recreate it.
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,key,name FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type=? AND deleted_at!=''`, tenantID, a.pid(), object)
	if err != nil {
		fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var tombstone FieldDefinition
		if err = rows.Scan(&tombstone.ID, &tombstone.Key, &tombstone.Name); err != nil {
			rows.Close()
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		defs = append(defs, tombstone)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	presets := []fieldPreset{}
	systemFields := []systemField{}
	catalog := []FieldDefinition{}
	if object == "requirement" {
		systemFields = requirementSystemFields()
		catalog = requirementFieldPresets()
	}
	available := 0
	for _, d := range catalog {
		p := fieldPreset{FieldDefinition: d}
		for _, old := range defs {
			if old.Key == d.Key || strings.TrimSpace(old.Name) == d.Name {
				p.Installed = true
				p.ExistingID = old.ID
				break
			}
		}
		if !p.Installed {
			available++
		}
		presets = append(presets, p)
	}
	_, _, _, active := a.currentUser(r)
	write(w, 200, map[string]any{"objectType": object, "systemFields": systemFields, "presets": presets, "availableCount": available, "canManage": active && a.canManageProject(a.uid(), a.pid())})
}
func (a *App) applyFieldPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	_, _, _, active := a.currentUser(r)
	if !active || !a.canManageProject(a.uid(), a.pid()) {
		fail(w, 403, "admin_required", "仅管理员可执行此操作")
		return
	}
	var body struct {
		ObjectType string   `json:"objectType"`
		Keys       []string `json:"keys"`
	}
	if decodeJSON(r, &body) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if body.ObjectType == "" {
		body.ObjectType = "requirement"
	}
	if !presetObject(body.ObjectType) {
		fail(w, 422, "validation_error", "当前对象暂无默认字段预设")
		return
	}
	catalog := requirementFieldPresets()
	selected := map[string]bool{}
	for _, key := range body.Keys {
		found := false
		for _, d := range catalog {
			if key == d.Key {
				found = true
				break
			}
		}
		if !found {
			fail(w, 422, "validation_error", "默认字段 key 无效")
			return
		}
		selected[key] = true
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	items := []FieldDefinition{}
	skipped := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, d := range catalog {
		if body.Keys != nil && !selected[d.Key] {
			continue
		}
		var id int64
		err = tx.QueryRow(`SELECT id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type=? AND (key=? OR trim(name)=?) ORDER BY id LIMIT 1`, tenantID, a.pid(), body.ObjectType, d.Key, d.Name).Scan(&id)
		if err == nil {
			skipped++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		res, err := tx.Exec(`INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), d.ObjectType, d.Key, d.Name, d.Type, d.Description, d.Required, d.Searchable, d.Filterable, d.ListVisible, d.Enabled, d.SortOrder, "null", jsonText(d.Options), "", now, now)
		if err != nil {
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		d.ID, err = res.LastInsertId()
		if err != nil {
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		items = append(items, d)
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"createdCount": len(items), "skippedCount": skipped, "items": items})
}
