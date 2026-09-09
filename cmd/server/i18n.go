package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const defaultLocale = "zh-CN"

var supportedLocales = []string{"zh-CN", "en-US"}
var languageRange = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
var languageQuality = regexp.MustCompile(`^(0(\.[0-9]{0,3})?|1(\.0{0,3})?)$`)

func storedLocale(locale string) string {
	if locale == "en-US" {
		return locale
	}
	return defaultLocale
}

// Only explicit, supported language ranges participate. A wildcard or an
// unsupported/malformed range leaves the account preference in control.
func acceptedLocale(header string) string {
	best, bestQuality := "", -1.0
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		tag := strings.TrimSpace(parts[0])
		if !languageRange.MatchString(tag) || len(parts) > 2 {
			continue
		}
		quality := 1.0
		if len(parts) == 2 {
			parameter := strings.SplitN(strings.TrimSpace(parts[1]), "=", 2)
			if len(parameter) != 2 || !strings.EqualFold(strings.TrimSpace(parameter[0]), "q") || !languageQuality.MatchString(strings.TrimSpace(parameter[1])) {
				continue
			}
			quality, _ = strconv.ParseFloat(strings.TrimSpace(parameter[1]), 64)
		}
		if quality <= 0 || quality <= bestQuality {
			continue
		}
		base := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		switch base {
		case "zh":
			best, bestQuality = "zh-CN", quality
		case "en":
			best, bestQuality = "en-US", quality
		}
	}
	return best
}

func negotiatedLocale(header, preference string) string {
	if locale := acceptedLocale(header); locale != "" {
		return locale
	}
	return storedLocale(preference)
}

func setResponseLocale(w http.ResponseWriter, locale string) {
	w.Header().Set("Content-Language", storedLocale(locale))
	for _, field := range []string{"Accept-Language", "Cookie"} {
		found := false
		for _, existing := range w.Header().Values("Vary") {
			for _, token := range strings.Split(existing, ",") {
				if strings.EqualFold(strings.TrimSpace(token), field) || strings.TrimSpace(token) == "*" {
					found = true
				}
			}
		}
		if !found {
			w.Header().Add("Vary", field)
		}
	}
}

// Locale is a tenant-wide personal preference, independent of project roles.
// Updating it deliberately does not reuse the full profile update handler.
func (a *App) localePreferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodGet {
		var locale string
		err := a.db.QueryRowContext(r.Context(), `SELECT locale FROM users WHERE tenant_id=? AND id=? AND active=1`, tenantID, a.uid()).Scan(&locale)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusUnauthorized, "unauthorized", "账号不可用")
			return
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "语言设置暂时无法读取，请稍后重试")
			return
		}
		write(w, http.StatusOK, map[string]any{"locale": storedLocale(locale), "supportedLocales": supportedLocales})
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	var body struct {
		Locale string `json:"locale"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	if !validChoice(body.Locale, supportedLocales) {
		fail(w, http.StatusUnprocessableEntity, "invalid_locale", "语言只支持 zh-CN 或 en-US")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE users SET locale=? WHERE tenant_id=? AND id=? AND active=1`, body.Locale, tenantID, a.uid())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "语言设置暂时无法保存，请稍后重试")
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "语言设置暂时无法保存，请稍后重试")
		return
	}
	if count == 0 {
		fail(w, http.StatusUnauthorized, "unauthorized", "账号不可用")
		return
	}
	setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), body.Locale))
	write(w, http.StatusOK, map[string]any{"locale": body.Locale, "supportedLocales": supportedLocales})
}

func localizedError(locale string, status int, code, message string) string {
	// Never return SQL text, filesystem paths or driver internals to clients.
	if code == "db_error" || code == "database_unavailable" {
		if locale == "en-US" {
			return "The data service is temporarily unavailable. Please try again."
		}
		return "数据服务暂时不可用，请稍后重试"
	}
	if locale != "en-US" {
		return message
	}
	if translated, ok := englishErrors[message]; ok {
		return translated
	}
	for _, template := range englishErrorTemplates {
		if matches := template.pattern.FindStringSubmatch(message); matches != nil {
			values := make([]any, len(matches)-1)
			for index, value := range matches[1:] {
				values[index] = value // Custom field names and other user data stay verbatim.
				if template.english == "The current status ‘%s’ cannot transition to ‘%s’." {
					if state := englishWorkflowStates[value]; state != "" {
						values[index] = state
					}
				}
			}
			return fmt.Sprintf(template.english, values...)
		}
	}
	if status >= 500 {
		return "The service is temporarily unavailable. Please try again."
	}
	switch status {
	case http.StatusUnauthorized:
		return "Please sign in again."
	case http.StatusForbidden:
		return "You do not have permission to perform this action."
	case http.StatusNotFound:
		return "The requested resource was not found."
	case http.StatusConflict:
		return "The request conflicts with the current data. Refresh and try again."
	default:
		return "The request is invalid. Please check the submitted fields."
	}
}

// Notification bodies are often user-authored comments or test results. Only
// these exact event-bound system templates may be translated; never walk and
// replace arbitrary response strings or the persisted notification payload.
func localizedNotification(locale, event, title, body string) (string, string) {
	if locale != "en-US" {
		return title, body
	}
	if titles := englishNotificationTitles[event]; titles != nil {
		if translated := titles[title]; translated != "" {
			title = translated
		}
	}
	parts := strings.SplitN(event, ".", 2)
	if len(parts) == 2 && validChoice(parts[0], []string{"requirement", "defect", "test_case", "test_plan", "test_execution", "sprint"}) {
		if parts[1] == "mentioned" && title == "你在评论中被提及" {
			title = "You were mentioned in a comment"
		} else if parts[1] == "replied" && title == "你的评论收到回复" {
			title = "Someone replied to your comment"
		} else if validChoice(parts[1], []string{"assigned", "assignee_changed"}) && title == "有新的工作项分配给你" {
			title = "A work item was assigned to you"
		} else if title == "工作项有新动态" && validChoice(parts[1], []string{"created", "updated", "status_changed", "assignee_changed"}) {
			title = "A work item has an update"
		}
	}
	switch event {
	case "automation.requirement_status_changed":
		// 仅翻译自动化自身的固定语法，需求编号、标题和自定义状态名属于业务数据，
		// 必须原样保留，不能把用户输入当作词典键做替换。
		if name, ok := strings.CutPrefix(title, "自动化规则："); ok {
			title = "Automation rule: " + name
		}
		if subject, rest, ok := strings.Cut(body, " 的状态已从「"); ok {
			if from, target, ok := strings.Cut(rest, "」变更为「"); ok && strings.HasSuffix(target, "」") {
				to := strings.TrimSuffix(target, "」")
				if translated := englishWorkflowStates[from]; translated != "" {
					from = translated
				}
				if translated := englishWorkflowStates[to]; translated != "" {
					to = translated
				}
				body = "Status changed from “" + from + "” to “" + to + "”: " + subject
			}
		}
	case "requirement.backend_completed", "requirement.frontend_completed":
		// 仅翻译事件绑定的固定首行；需求编号和业务标题始终保持原文。
		if header, suffix, found := strings.Cut(body, "\n"); found {
			if event == "requirement.backend_completed" && header == "该需求后端已完成，请对口前端工程师确认联调与后续开发。" {
				body = "Backend work is complete. Assigned frontend engineers should coordinate integration and next steps.\n" + suffix
			} else if event == "requirement.frontend_completed" && header == "该需求前端已完成，请对口后端工程师确认联调与后续开发。" {
				body = "Frontend work is complete. Assigned backend engineers should coordinate integration and next steps.\n" + suffix
			}
		}
	case "requirement.updated":
		if header, suffix, found := strings.Cut(body, "\n"); found {
			if fields, ok := strings.CutPrefix(header, "需求已更新："); ok {
				body = "Requirement updated: " + fields + "\n" + suffix
			}
		}
	case "requirement.status_changed":
		if header, suffix, found := strings.Cut(body, "\n"); found {
			_, translated := localizedNotification(locale, event, "", header)
			return title, translated + "\n" + suffix
		}
		if name, ok := strings.CutPrefix(body, "需求状态已变更为「"); ok && strings.HasSuffix(name, "」") {
			body = "Requirement status changed to “" + strings.TrimSuffix(name, "」") + "”"
		} else if name, ok := strings.CutPrefix(body, "需求状态已变更为 "); ok {
			if translated := englishWorkflowStates[name]; translated != "" {
				name = translated
			}
			body = "Requirement status changed to " + name
		} else if label := englishWorkflowStates[body]; label != "" {
			body = label
		}
	case "sprint.status_changed":
		if value, ok := strings.CutPrefix(body, "迭代状态已变更为 "); ok {
			if label := englishWorkflowStates[value]; label != "" {
				body = "Sprint status changed to " + label
			}
		}
	case "defect.status_changed":
		if parts := defectStatusNotice.FindStringSubmatch(body); parts != nil {
			if label := englishWorkflowStates[parts[2]]; label != "" {
				body = parts[1] + " moved to " + label
			}
		}
	case "sprint.completed":
		if parts := sprintCompletedNotice.FindStringSubmatch(body); parts != nil {
			body = fmt.Sprintf("Sprint completed: moved %s requirements and %s defects to %s", parts[1], parts[2], parts[3])
		}
	case "defect.created_from_execution":
		if parts := defectCreatedNotice.FindStringSubmatch(body); parts != nil {
			body = parts[1] + " created " + parts[2]
		}
	}
	return title, body
}

func localizedOutboxPayload(locale, event, raw string) string {
	if locale != "en-US" {
		return raw
	}
	var payload map[string]any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return raw
	}
	title, hasTitle := payload["title"].(string)
	body, hasBody := payload["body"].(string)
	translatedTitle, translatedBody := localizedNotification(locale, event, title, body)
	if title == translatedTitle && body == translatedBody {
		return raw
	}
	if hasTitle {
		payload["title"] = translatedTitle
	}
	if hasBody {
		payload["body"] = translatedBody
	}
	return jsonText(payload)
}

var defectStatusNotice = regexp.MustCompile(`^(BUG-[0-9]+) 已流转至(.+)$`)
var sprintCompletedNotice = regexp.MustCompile(`(?s)^完成迭代，将 ([0-9]+) 个需求、([0-9]+) 个缺陷迁移到 (.*)$`)
var defectCreatedNotice = regexp.MustCompile(`^(EXE-[0-9]+) 已生成 (BUG-[0-9]+)$`)

var englishWorkflowStates = map[string]string{
	"已上线": "Released", "待上线": "Ready for release", "后端已完成": "Backend complete", "前端已完成": "Frontend complete", "实现中": "Implementing", "开发完成": "Development complete", "流程挂起": "On hold", "流程终止": "Terminated", "后端完成 | 前端开发中": "Backend complete | Frontend in development", "前端完成 | 后端开发中": "Frontend complete | Backend in development", "冒烟测试完成": "Smoke testing complete",
	"规划中": "Planning", "进行中": "In progress", "已完成": "Completed", "已取消": "Cancelled",
	"新建": "New", "待确认": "Pending confirmation", "修复中": "In progress", "待验证": "Ready for verification", "已关闭": "Closed", "重新打开": "Reopened", "已拒绝": "Rejected",
	"草稿": "Draft", "评审中": "In review", "待开发": "Ready for development", "开发中": "In development", "测试中": "In testing",
	"已确认": "Confirmed", "已解决": "Resolved",
}

var englishNotificationTitles = map[string]map[string]string{
	"requirement.backend_completed":     {"后端已完成，待前端协作": "Backend complete — frontend handoff"},
	"requirement.frontend_completed":    {"前端已完成，待后端协作": "Frontend complete — backend handoff"},
	"requirement.description_mentioned": {"你在需求正文中被提及": "You were mentioned in a requirement description"},
	"requirement.remarks_mentioned":     {"你在需求备注中被提及": "You were mentioned in requirement remarks"},
	"requirement.mentioned":             {"你在评论中被提及": "You were mentioned in a comment"},
	"requirement.assigned":              {"有新的工作项分配给你": "A work item was assigned to you", "你有新的高优先级需求": "A high-priority requirement was assigned to you"},
	"requirement.created":               {"工作项有新动态": "A work item has an update"},
	"requirement.updated":               {"工作项有新动态": "A work item has an update"},
	"requirement.status_changed":        {"工作项有新动态": "A work item has an update"},
	"defect.created":                    {"工作项有新动态": "A work item has an update"},
	"defect.assigned":                   {"有新的工作项分配给你": "A work item was assigned to you"},
	"defect.status_changed":             {"缺陷状态已更新": "Defect status updated", "缺陷已就绪，等待验证": "Defect ready for verification"},
	"defect.verification":               {"缺陷等待回归验证": "Defect awaiting regression testing"},
	"defect.created_from_execution":     {"测试失败已生成缺陷": "A defect was created from a failed test"},
	"test.failed":                       {"测试执行失败": "Test execution failed"},
	"sprint.status_changed":             {"迭代状态已更新": "Sprint status updated"},
	"sprint.completed":                  {"迭代已完成": "Sprint completed"},
}
