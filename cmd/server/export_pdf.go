package main

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/signintech/gopdf"
)

// The redistributable font is embedded so Linux deployments need no host fonts.
//
//go:embed assets/NotoSansSC-Regular.ttf
var exportFont []byte

var errPDFBudget = errors.New("PDF resource budget exceeded")
var pdfExportSlots = make(chan struct{}, 2)

const maxPDFImageBytes = 20 << 20
const maxPDFImagePixels = 48000000
const maxPDFImages = 80

type pdfBlock struct {
	title, text string
	image       []byte
}
type workPDF struct {
	code, title, project, kind string
	blocks                     []pdfBlock
	imageBytes                 int64
	imagePixels                int64
	imageCount                 int
}

func (a *App) exportPDF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/exports/"), "/")
	if len(parts) != 2 || !strings.HasSuffix(parts[1], ".pdf") || !validChoice(parts[0], []string{"requirement", "defect"}) {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(parts[1], ".pdf"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	select {
	case pdfExportSlots <- struct{}{}:
		defer func() { <-pdfExportSlots }()
	default:
		fail(w, 503, "export_unavailable", "导出暂时不可用，请稍后重试")
		return
	}
	document, err := a.workPDF(parts[0], id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "资源不存在")
		} else if errors.Is(err, errPDFBudget) {
			fail(w, 422, "pdf_export_failed", "PDF 生成失败，内容或图片过大，请调整后重试")
		} else {
			fail(w, 503, "export_unavailable", "导出暂时不可用，请稍后重试")
		}
		return
	}
	data, err := renderWorkPDF(document, w.Header().Get("Content-Language") == "en-US")
	if err != nil {
		fail(w, 422, "pdf_export_failed", "PDF 生成失败，内容或图片过大，请调整后重试")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	filename := fmt.Sprintf("BUG-%04d.pdf", id)
	if parts[0] == "requirement" {
		// document.code is normalized from the canonical requirement ID in
		// workPDF, including records that still store a historic REQ-* value.
		filename = document.code + ".pdf"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func pdfDate(value string) string {
	if value == "" {
		return "—"
	}
	date, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return date.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05") + " +08:00"
}
func (a *App) workPDF(object string, id int64) (workPDF, error) {
	out := workPDF{kind: object}
	if err := a.db.QueryRow(`SELECT name FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&out.project); err != nil {
		return out, err
	}
	people := map[string]string{}
	rows, err := a.db.Query(`SELECT u.id,u.name FROM users u JOIN project_members m ON m.user_id=u.id AND m.tenant_id=u.tenant_id WHERE u.tenant_id=? AND m.project_id=?`, tenantID, a.pid())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return out, err
		}
		people[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	names := func(ids []string, fallback string) string {
		result := []string{}
		for _, id := range ids {
			name := people[id]
			if name == "" {
				name = id
			}
			result = append(result, name)
		}
		if len(result) == 0 {
			return fallback
		}
		return strings.Join(result, "、")
	}
	add := func(title, text string) { out.blocks = append(out.blocks, pdfBlock{title: title, text: text}) }
	if object == "requirement" {
		x, err := a.get(id)
		if err != nil {
			return out, err
		}
		out.code = x.Code
		if out.code == "" {
			out.code = requirementDisplayCode(id, "")
		}
		out.title = x.Title
		add("基本信息 / Information", fmt.Sprintf("状态: %s    优先级: %s    分类: %s\n迭代: %s\n处理人: %s\n产品负责人: %s\n创建时间: %s\n更新时间: %s", x.StatusName, x.Priority, x.Category, x.Sprint, names(x.AssigneeUserIDs, x.Assignee), names(x.OwnerUserIDs, x.Owner), pdfDate(x.CreatedAt), pdfDate(x.UpdatedAt)))
		if x.ParentID != nil {
			var title, code string
			err = a.db.QueryRow(`SELECT code,title FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), *x.ParentID).Scan(&code, &title)
			if err == nil {
				add("父需求 / Parent", requirementDisplayCode(*x.ParentID, code)+" · "+title)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
		}
		add("需求描述 / Description", "")
		if len(x.DescriptionDoc) > 0 {
			doc, err := parseRichDocument(x.DescriptionDoc)
			if err != nil {
				return out, err
			}
			if doc != nil {
				if err = a.pdfRichBlocks(&out, id, doc.root); err != nil {
					return out, err
				}
			} else {
				add("", x.Description)
			}
		} else {
			add("", x.Description)
		}
		add("验收标准 / Acceptance criteria", x.Acceptance)
		labels := []string{"前端", "后端", "算法", "UI", "产品"}
		weightLines := []string{}
		for index, role := range requirementWeightRoles {
			weight := x.RoleWeights[role]
			number := "未评估"
			if weight.Value != nil {
				number = strconv.FormatFloat(*weight.Value, 'f', -1, 64)
			}
			weightLines = append(weightLines, fmt.Sprintf("%s: %s    人员: %s", labels[index], number, names(weight.UserIDs, "—")))
		}
		weightLines = append(weightLines, fmt.Sprintf("总权重: %g", x.WeightTotal))
		add("角色权重 / Role weights", strings.Join(weightLines, "\n"))
		add("标签 / Tags", x.Tags)
		add("备注 / Remarks", x.Remarks)
		add("计划 / Plan", fmt.Sprintf("计划开始: %s    计划结束: %s\n进度: %d%%    预估工时: %g h    实际工时: %g h", x.StartDate, x.EndDate, x.Progress, x.EstimatedHours, x.ActualHours))
		if err = a.pdfResources(&out, id); err != nil {
			return out, err
		}
	} else {
		x, err := a.getDefect(id)
		if err != nil {
			return out, err
		}
		out.code = x.Code
		if out.code == "" {
			out.code = fmt.Sprintf("BUG-%04d", id)
		}
		out.title = x.Title
		var createdAt string
		if err = a.db.QueryRow(`SELECT created_at FROM defects WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&createdAt); err != nil {
			return out, err
		}
		add("基本信息 / Information", fmt.Sprintf("状态: %s    严重程度: %s    优先级: %s\n负责人: %s    验证人: %s\n迭代: %s\n创建时间: %s\n更新时间: %s", x.Status, x.Severity, x.Priority, x.Assignee, x.Verifier, x.Sprint, pdfDate(createdAt), pdfDate(x.UpdatedAt)))
		if x.RequirementID != nil {
			var code, title string
			err = a.db.QueryRow(`SELECT code,title FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), *x.RequirementID).Scan(&code, &title)
			if err == nil {
				add("关联需求 / Requirement", requirementDisplayCode(*x.RequirementID, code)+" · "+title)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
		}
		add("缺陷描述 / Description", x.Description)
		add("复现步骤 / Reproduction steps", x.Steps)
		add("实际结果 / Actual result", x.Actual)
		add("预期结果 / Expected result", x.Expected)
		add("环境与版本 / Environment", fmt.Sprintf("%s\n发现版本: %s    修复版本: %s", x.Environment, x.FoundVersion, x.FixVersion))
		add("标签 / Tags", x.Tags)
	}
	rows, err = a.db.Query(`SELECT d.name,d.type,v.value_json FROM field_values v JOIN field_definitions d ON d.id=v.field_definition_id AND d.tenant_id=v.tenant_id AND d.project_id=v.project_id AND d.deleted_at='' WHERE v.tenant_id=? AND v.project_id=? AND v.object_type=? AND v.object_id=? ORDER BY d.sort_order,d.id`, tenantID, a.pid(), object, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	custom := []string{}
	for rows.Next() {
		var name, kind, raw string
		if err = rows.Scan(&name, &kind, &raw); err != nil {
			return out, err
		}
		var value any
		if err = json.Unmarshal([]byte(raw), &value); err != nil {
			return out, err
		}
		text := ""
		if ids, ok := fieldPersonIDs(value, kind == "users"); (kind == "user" || kind == "users") && ok {
			text = names(ids, "")
		} else {
			switch v := value.(type) {
			case nil:
			case string:
				text = v
			case []any:
				parts := []string{}
				for _, item := range v {
					parts = append(parts, fmt.Sprint(item))
				}
				text = strings.Join(parts, "、")
			default:
				text = fmt.Sprint(v)
			}
		}
		custom = append(custom, name+": "+text)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(custom) > 0 {
		add("自定义字段 / Custom fields", strings.Join(custom, "\n"))
	}
	return out, nil
}

func (a *App) pdfRichBlocks(out *workPDF, requirementID int64, node *richNode) error {
	if node.Type == "image" {
		id, _ := richInteger(node.Attrs, "attachmentId", 0)
		name, _ := richString(node.Attrs, "name")
		var data []byte
		var size int64
		err := a.db.QueryRow(`SELECT length(content) FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&size)
		if errors.Is(err, sql.ErrNoRows) {
			out.blocks = append(out.blocks, pdfBlock{text: "[图片已移除] " + name})
			return nil
		}
		if err != nil {
			return err
		}
		if size <= 0 || size > 10<<20 || out.imageBytes+size > maxPDFImageBytes || out.imageCount >= maxPDFImages {
			return errPDFBudget
		}
		if err = a.db.QueryRow(`SELECT content FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=? AND length(content)<=?`, tenantID, a.pid(), requirementID, id, 10<<20).Scan(&data); err != nil {
			return err
		}
		pixels, err := validatePDFImage(data)
		if err != nil {
			return err
		}
		if out.imagePixels+pixels > maxPDFImagePixels {
			return errPDFBudget
		}
		out.imagePixels += pixels
		out.imageBytes += size
		out.imageCount++
		out.blocks = append(out.blocks, pdfBlock{text: name, image: data})
		return nil
	}
	if validChoice(node.Type, []string{"paragraph", "heading", "codeBlock", "attachment"}) {
		text := (&richDocument{root: node}).plainText()
		if node.Type == "heading" {
			out.blocks = append(out.blocks, pdfBlock{title: text})
		} else {
			out.blocks = append(out.blocks, pdfBlock{text: text})
		}
		return nil
	}
	for _, child := range node.Content {
		if err := a.pdfRichBlocks(out, requirementID, child); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) pdfResources(out *workPDF, id int64) error {
	rows, err := a.db.Query(`SELECT name,size_bytes FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? ORDER BY id`, tenantID, a.pid(), id)
	if err != nil {
		return err
	}
	parts := []string{}
	for rows.Next() {
		var name string
		var size int64
		if err = rows.Scan(&name, &size); err != nil {
			rows.Close()
			return err
		}
		parts = append(parts, fmt.Sprintf("%s (%g KB)", name, float64(size)/1024))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(parts) > 0 {
		out.blocks = append(out.blocks, pdfBlock{title: "附件 / Attachments", text: strings.Join(parts, "\n")})
	}
	rows, err = a.db.Query(`SELECT title,url FROM requirement_design_links WHERE tenant_id=? AND project_id=? AND requirement_id=? ORDER BY id`, tenantID, a.pid(), id)
	if err != nil {
		return err
	}
	defer rows.Close()
	parts = []string{}
	for rows.Next() {
		var title, url string
		if err = rows.Scan(&title, &url); err != nil {
			return err
		}
		parts = append(parts, title+"\n"+url)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(parts) > 0 {
		out.blocks = append(out.blocks, pdfBlock{title: "设计文件 / Design files", text: strings.Join(parts, "\n")})
	}
	return nil
}

func validatePDFImage(data []byte) (int64, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validChoice(format, []string{"png", "jpeg", "gif"}) || config.Width <= 0 || config.Height <= 0 || config.Width > 12000 || config.Height > 12000 {
		return 0, errPDFBudget
	}
	pixels := int64(config.Width) * int64(config.Height)
	if pixels > 24000000 {
		return 0, errPDFBudget
	}
	return pixels, nil
}

func renderWorkPDF(document workPDF, english bool) (result []byte, err error) {
	// Reject oversized content before expensive font shaping/image decoding.
	textBytes := len(document.title) + len(document.project) + len(document.code)
	imageBytes, imageCount := 0, 0
	var imagePixels int64
	if len(document.blocks) > 5000 {
		return nil, errPDFBudget
	}
	for _, block := range document.blocks {
		textBytes += len(block.text) + len(block.title)
		if len(block.image) > 0 {
			imageBytes += len(block.image)
			imageCount++
			pixels, e := validatePDFImage(block.image)
			if e != nil {
				return nil, e
			}
			imagePixels += pixels
		}
		if textBytes > 1<<20 || imageBytes > maxPDFImageBytes || imageCount > maxPDFImages || imagePixels > maxPDFImagePixels {
			return nil, errPDFBudget
		}
	}
	defer func() {
		if recover() != nil {
			result = nil
			err = errPDFBudget
		}
	}()
	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontDataWithOption("Noto", exportFont, gopdf.TtfOption{OnGlyphNotFoundSubstitute: func(r rune) rune { return '□' }}); err != nil {
		return nil, err
	}
	const left = 42.0
	const width = 511.0
	const bottom = 780.0
	page := 0
	y := 70.0
	var failure error
	setFont := func(size float64) {
		if err := pdf.SetFont("Noto", "", size); err != nil {
			failure = err
		}
	}
	newPage := func() {
		if failure != nil {
			return
		}
		page++
		if page > 250 {
			failure = fmt.Errorf("too many PDF pages")
			return
		}
		pdf.AddPage()
		setFont(9)
		pdf.SetTextColor(103, 116, 139)
		pdf.SetXY(left, 26)
		if failure = pdf.Cell(nil, "TaskLoom  /  "+document.code); failure != nil {
			return
		}
		pdf.SetXY(left, 810)
		if failure = pdf.Cell(nil, fmt.Sprintf("%d  ·  %s", page, time.Now().UTC().Format("2006-01-02 15:04 UTC"))); failure != nil {
			return
		}
		pdf.SetStrokeColor(220, 226, 237)
		pdf.Line(left, 48, left+width, 48)
		y = 68
	}
	clean := func(value string) string {
		return strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) && r != '\n' {
				return -1
			}
			return r
		}, value)
	}
	text := func(value string, size float64) {
		if failure != nil || strings.TrimSpace(value) == "" {
			return
		}
		setFont(size)
		lines, err := pdf.SplitText(clean(value), width)
		if err != nil {
			failure = err
			return
		}
		height := size * 1.7
		for _, line := range lines {
			if y+height > bottom {
				newPage()
			}
			if failure != nil {
				return
			}
			setFont(size)
			pdf.SetTextColor(43, 55, 77)
			pdf.SetXY(left, y)
			if line != "" {
				if err := pdf.CellWithOption(&gopdf.Rect{W: width, H: height}, line, gopdf.CellOption{Align: gopdf.Left | gopdf.Top}); err != nil {
					failure = err
					return
				}
			}
			y += height
		}
	}
	newPage()
	text(document.project+"  ·  "+document.code, 10)
	y += 8
	text(document.title, 20)
	y += 16
	for _, block := range document.blocks {
		if block.title != "" {
			if y+72 > bottom {
				newPage()
			}
			y += 10
			title := block.title
			if english && strings.Contains(title, " / ") {
				title = strings.SplitN(title, " / ", 2)[1]
			}
			text(title, 12)
			y += 6
		}
		if len(block.image) > 0 {
			config, _, err := image.DecodeConfig(bytes.NewReader(block.image))
			if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 24000000 {
				return nil, fmt.Errorf("unsupported image")
			}
			w, h := float64(config.Width), float64(config.Height)
			scale := 1.0
			if w > width {
				scale = width / w
			}
			if h*scale > 570 {
				scale = 570 / h
			}
			w *= scale
			h *= scale
			if y+h+30 > bottom {
				newPage()
			}
			holder, err := gopdf.ImageHolderByBytes(block.image)
			if err != nil {
				return nil, err
			}
			if err = pdf.ImageByHolder(holder, left, y, &gopdf.Rect{W: w, H: h}); err != nil {
				return nil, err
			}
			y += h + 8
			text(block.text, 9)
			y += 6
		} else {
			text(block.text, 10.5)
			y += 8
		}
		if failure != nil {
			return nil, failure
		}
	}
	if failure != nil {
		return nil, failure
	}
	return pdf.GetBytesPdfReturnErr()
}
