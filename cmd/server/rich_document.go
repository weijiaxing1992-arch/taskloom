package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const richRequestMaxSize int64 = 30 << 20
const richPendingMaxSize = 20 << 20
const richMaxNodes = 5000
const richMaxDepth = 24
const richMaxText = 100000

type richValidationError string

func (e richValidationError) Error() string { return string(e) }

type richNode struct {
	Type    string                     `json:"type"`
	Attrs   map[string]json.RawMessage `json:"attrs,omitempty"`
	Content []*richNode                `json:"content,omitempty"`
	Text    string                     `json:"text,omitempty"`
	Marks   []richMark                 `json:"marks,omitempty"`
}
type richMark struct {
	Type  string                     `json:"type"`
	Attrs map[string]json.RawMessage `json:"attrs,omitempty"`
}
type richFile struct {
	node    *richNode
	content []byte
	name    string
	id      int64
	pending bool
}
type richDocument struct {
	root                         *richNode
	files                        []richFile
	mentions                     []*richNode
	nodes, textSize, pendingSize int
}

func richRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func richIsNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
func failRichDocument(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "需求不存在")
		return
	}
	var invalid richValidationError
	if errors.As(err, &invalid) {
		fail(w, 422, "invalid_rich_document", err.Error())
		return
	}
	fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
}

func (a *App) migrateRequirementRichDocuments() error {
	for _, field := range []struct{ table, column string }{{"requirements", "description_doc_json"}, {"comments", "content_doc_json"}} {
		var found int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, field.table, field.column).Scan(&found); err != nil {
			return err
		}
		if found == 0 {
			if _, err := a.db.Exec(`ALTER TABLE ` + field.table + ` ADD COLUMN ` + field.column + ` TEXT`); err != nil {
				return err
			}
		}
	}
	return nil
}

func isRichDocumentWrite(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "requirements" && r.Method == http.MethodPost {
		return true
	}
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "requirements" {
		return false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id < 1 {
		return false
	}
	return len(parts) == 3 && r.Method == http.MethodPatch || len(parts) == 4 && parts[3] == "comments" && r.Method == http.MethodPost
}

// 只接收受限 Tiptap JSON，不把 HTML 当正文执行；统一限制节点、层级、文本和待上传二进制预算。
func parseRichDocument(raw json.RawMessage) (*richDocument, error) {
	if richIsNull(raw) {
		return nil, nil
	}
	if len(raw) > int(richRequestMaxSize) {
		return nil, richValidationError("富文本内容不能超过 30 MB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	doc := &richDocument{}
	if err := decoder.Decode(&doc.root); err != nil || doc.root == nil || doc.root.Type != "doc" {
		return nil, richValidationError("富文本格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, richValidationError("富文本格式无效")
	}
	if err := doc.validateNode(doc.root, "", 0); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(doc.plainText()) > richMaxText {
		return nil, richValidationError("富文本纯文本不能超过 100000 字")
	}
	return doc, nil
}

func readRichDocument(raw sql.NullString) (json.RawMessage, error) {
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	doc, err := parseRichDocument(json.RawMessage(raw.String))
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}
	for _, file := range doc.files {
		if file.pending {
			return nil, fmt.Errorf("persisted document contains pending binary")
		}
	}
	return richRaw(doc.root), nil
}

func richAttrs(node *richNode, allowed ...string) error {
	for key := range node.Attrs {
		if !validChoice(key, allowed) {
			return richValidationError("富文本包含不支持的属性")
		}
	}
	return nil
}
func richString(attrs map[string]json.RawMessage, key string) (string, error) {
	var value string
	if raw, ok := attrs[key]; ok {
		if json.Unmarshal(raw, &value) != nil {
			return "", richValidationError("富文本属性格式无效")
		}
	}
	return value, nil
}
func richInteger(attrs map[string]json.RawMessage, key string, fallback int64) (int64, error) {
	raw, ok := attrs[key]
	if !ok || richIsNull(raw) {
		return fallback, nil
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil {
		return 0, richValidationError("富文本属性格式无效")
	}
	return value, nil
}

func (d *richDocument) validateNode(node *richNode, parent string, depth int) error {
	if node == nil {
		return richValidationError("富文本格式无效")
	}
	d.nodes++
	if d.nodes > richMaxNodes || depth > richMaxDepth {
		return richValidationError("富文本节点过多或层级过深")
	}
	block := validChoice(node.Type, []string{"paragraph", "heading", "bulletList", "orderedList", "blockquote", "codeBlock", "horizontalRule", "image", "attachment", "table"})
	inline := validChoice(node.Type, []string{"text", "hardBreak", "mention"})
	if parent == "" && node.Type != "doc" || parent == "doc" && !block || (parent == "paragraph" || parent == "heading") && !inline || parent == "codeBlock" && node.Type != "text" || (parent == "bulletList" || parent == "orderedList") && node.Type != "listItem" || (parent == "listItem" || parent == "blockquote") && !block {
		return richValidationError("富文本节点结构无效")
	}
	if parent == "table" && node.Type != "tableRow" || parent == "tableRow" && node.Type != "tableCell" && node.Type != "tableHeader" || (parent == "tableCell" || parent == "tableHeader") && (!block || node.Type == "table") {
		return richValidationError("富文本表格结构无效")
	}
	if node.Type != "text" && node.Text != "" {
		return richValidationError("富文本节点结构无效")
	}
	if !inline && len(node.Marks) > 0 {
		return richValidationError("富文本节点结构无效")
	}
	if err := validateRichMarks(node); err != nil {
		return err
	}
	allowedAttrs := []string{}
	switch node.Type {
	case "table", "tableRow", "tableCell", "tableHeader":
		if node.Type == "tableCell" || node.Type == "tableHeader" {
			allowedAttrs = []string{"textAlign"}
			if raw, ok := node.Attrs["textAlign"]; ok && !richIsNull(raw) {
				alignment, e := richString(node.Attrs, "textAlign")
				if e != nil || !validChoice(alignment, []string{"left", "center", "right"}) {
					return richValidationError("表格对齐无效")
				}
			}
		}
		if len(node.Content) == 0 || node.Type == "tableRow" && len(node.Content) > 30 || node.Type == "table" && len(node.Content) > 300 {
			return richValidationError("表格不能为空，最多支持 300 行、30 列")
		}
		if node.Type == "table" {
			width := -1
			for _, row := range node.Content {
				if row == nil || width != -1 && width != len(row.Content) {
					return richValidationError("表格每行列数必须一致")
				}
				width = len(row.Content)
			}
		}
	case "doc", "paragraph", "blockquote":
	case "heading":
		allowedAttrs = []string{"level"}
		level, err := richInteger(node.Attrs, "level", 1)
		if err != nil || level < 1 || level > 6 {
			return richValidationError("标题级别只能为 1–6")
		}
		if err := richAttrs(node, allowedAttrs...); err != nil {
			return err
		}
		node.Attrs = map[string]json.RawMessage{"level": richRaw(level)}
	case "bulletList", "orderedList":
		if len(node.Content) == 0 {
			return richValidationError("富文本列表不能为空")
		}
		if node.Type == "orderedList" {
			allowedAttrs = []string{"start"}
			start, err := richInteger(node.Attrs, "start", 1)
			if err != nil || start < 1 || start > 1000000 {
				return richValidationError("有序列表起始编号无效")
			}
			if err := richAttrs(node, allowedAttrs...); err != nil {
				return err
			}
			node.Attrs = map[string]json.RawMessage{"start": richRaw(start)}
		}
	case "listItem":
		if len(node.Content) == 0 || node.Content[0] == nil || node.Content[0].Type != "paragraph" {
			return richValidationError("富文本列表项必须以段落开始")
		}
	case "codeBlock":
		allowedAttrs = []string{"language"}
		if raw, ok := node.Attrs["language"]; ok && !richIsNull(raw) {
			language, err := richString(node.Attrs, "language")
			if err != nil || len(language) > 64 {
				return richValidationError("代码块语言无效")
			}
			for _, c := range language {
				if !(unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("_+-#.", c)) {
					return richValidationError("代码块语言无效")
				}
			}
		}
	case "text":
		if len(node.Content) > 0 || node.Text == "" || !utf8.ValidString(node.Text) {
			return richValidationError("富文本格式无效")
		}
		d.textSize += utf8.RuneCountInString(node.Text)
	case "hardBreak", "horizontalRule":
		if len(node.Content) > 0 {
			return richValidationError("富文本节点结构无效")
		}
	case "mention":
		allowedAttrs = []string{"id", "label"}
		if len(node.Content) > 0 {
			return richValidationError("富文本节点结构无效")
		}
		id, err := richString(node.Attrs, "id")
		if err != nil || id == "" || len(id) > 128 {
			return richValidationError("富文本提及成员无效")
		}
		label, err := richString(node.Attrs, "label")
		if err != nil || label == "" || utf8.RuneCountInString(label) > 200 {
			return richValidationError("富文本提及成员无效")
		}
		d.mentions = append(d.mentions, node)
		d.textSize += utf8.RuneCountInString(label) + 1
	case "image", "attachment":
		allowedAttrs = []string{"attachmentId", "name", "alt", "data", "category"}
		category, categoryErr := richString(node.Attrs, "category")
		if categoryErr != nil || (category != "" && !validAssetCategory(category)) {
			return richValidationError("附件分类无效")
		}
		if len(node.Content) > 0 {
			return richValidationError("富文本节点结构无效")
		}
		name, err := richString(node.Attrs, "name")
		if err != nil || !safeAttachmentFilename(name) {
			return richValidationError("附件文件名无效或包含路径")
		}
		alt, err := richString(node.Attrs, "alt")
		if err != nil || utf8.RuneCountInString(alt) > 1000 {
			return richValidationError("附件替代文本过长或无效")
		}
		d.textSize += utf8.RuneCountInString(name) + utf8.RuneCountInString(alt)
		id, err := richInteger(node.Attrs, "attachmentId", 0)
		if err != nil || id < 0 {
			return richValidationError("附件编号无效")
		}
		_, pending := node.Attrs["data"]
		file := richFile{node: node, name: name, id: id, pending: pending}
		if id > 0 && pending || id == 0 && !pending {
			return richValidationError("附件必须提供已有编号或待保存数据，不能同时提供")
		}
		if pending {
			if richIsNull(node.Attrs["data"]) {
				return richValidationError("附件数据必须是标准 Base64 内容")
			}
			encoded, err := richString(node.Attrs, "data")
			if err != nil {
				return err
			}
			if len(encoded) > base64.StdEncoding.EncodedLen(int(requirementAttachmentMaxSize)) {
				return richValidationError("单个附件不能超过 10 MB")
			}
			if strings.ContainsAny(encoded, " \t\r\n") {
				return richValidationError("附件数据必须是标准 Base64 内容")
			}
			file.content, err = base64.StdEncoding.Strict().DecodeString(encoded)
			if err != nil {
				return richValidationError("附件数据必须是标准 Base64 内容")
			}
			if len(file.content) > int(requirementAttachmentMaxSize) {
				return richValidationError("单个附件不能超过 10 MB")
			}
			d.pendingSize += len(file.content)
			if d.pendingSize > richPendingMaxSize {
				return richValidationError("一次保存的附件总大小不能超过 20 MB")
			}
			if node.Type == "image" {
				if err := validateRichImage(file.content); err != nil {
					return err
				}
			}
		}
		d.files = append(d.files, file)
	default:
		return richValidationError("富文本包含不支持的节点")
	}
	if err := richAttrs(node, allowedAttrs...); err != nil {
		return err
	}
	if d.textSize > richMaxText {
		return richValidationError("富文本纯文本不能超过 100000 字")
	}
	for _, child := range node.Content {
		if err := d.validateNode(child, node.Type, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateRichMarks(node *richNode) error {
	if len(node.Marks) > 8 {
		return richValidationError("富文本样式过多")
	}
	seen := map[string]bool{}
	for i, mark := range node.Marks {
		if seen[mark.Type] || !validChoice(mark.Type, []string{"bold", "italic", "underline", "strike", "code", "link"}) {
			return richValidationError("富文本包含不支持的样式")
		}
		seen[mark.Type] = true
		if mark.Type != "link" {
			if len(mark.Attrs) > 0 {
				return richValidationError("富文本包含不支持的属性")
			}
			continue
		}
		for key := range mark.Attrs {
			if !validChoice(key, []string{"href", "target", "rel", "class"}) {
				return richValidationError("富文本包含不支持的属性")
			}
		}
		href, err := richString(mark.Attrs, "href")
		if err != nil {
			return err
		}
		if !safeRichLink(href) {
			return richValidationError("链接只支持无凭据的 HTTP、HTTPS 或 mailto 地址")
		}
		node.Marks[i].Attrs = map[string]json.RawMessage{"href": richRaw(href)}
	}
	return nil
}
func safeRichLink(href string) bool {
	if href == "" || len(href) > 2048 || strings.TrimSpace(href) != href || strings.Contains(href, "\\") {
		return false
	}
	for _, c := range href {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return false
		}
	}
	u, err := url.Parse(href)
	if err != nil || u.User != nil {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Host != "" && u.Opaque == ""
	}
	return u.Scheme == "mailto" && u.Host == "" && u.Opaque != "" && !strings.ContainsAny(u.Opaque, "<>\" ")
}

// 不信任扩展名或客户端 MIME：解析真实 PNG/JPEG/GIF，并限制像素/动画预算；SVG 不进入图片渲染链。
func validateRichImage(data []byte) error {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validChoice(format, []string{"png", "jpeg", "gif"}) {
		return richValidationError("图片只支持有效的 PNG、JPEG 或 GIF，不能使用 SVG")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 12000 || config.Height > 12000 || int64(config.Width)*int64(config.Height) > 40000000 {
		return richValidationError("图片尺寸不得超过 12000 像素或 4000 万像素")
	}
	if format == "gif" {
		if err := checkGIFBudget(data); err != nil {
			return err
		}
		_, err = gif.DecodeAll(bytes.NewReader(data))
	} else {
		_, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return richValidationError("图片只支持有效的 PNG、JPEG 或 GIF，不能使用 SVG")
	}
	return nil
}

// Preflight all GIF frame descriptors before DecodeAll allocates pixel buffers.
// This blocks tiny compressed GIFs claiming hundreds of huge frames.
func checkGIFBudget(data []byte) error {
	invalid := richValidationError("GIF 帧数或累计像素过多，或文件内容无效")
	if len(data) < 13 {
		return invalid
	}
	offset := 13
	if data[10]&0x80 != 0 {
		offset += 3 * (1 << ((data[10] & 7) + 1))
	}
	frames := 0
	var pixels int64
	skipBlocks := func() bool {
		for {
			if offset >= len(data) {
				return false
			}
			n := int(data[offset])
			offset++
			if n == 0 {
				return true
			}
			offset += n
			if offset > len(data) {
				return false
			}
		}
	}
	for offset < len(data) {
		kind := data[offset]
		offset++
		switch kind {
		case 0x3b:
			if frames > 0 && offset == len(data) {
				return nil
			}
			return invalid
		case 0x21:
			offset++
			if offset > len(data) || !skipBlocks() {
				return invalid
			}
		case 0x2c:
			if offset+9 > len(data) {
				return invalid
			}
			width := binary.LittleEndian.Uint16(data[offset+4:])
			height := binary.LittleEndian.Uint16(data[offset+6:])
			packed := data[offset+8]
			offset += 9
			frames++
			pixels += int64(width) * int64(height)
			if frames > 500 || pixels > 40000000 {
				return invalid
			}
			if packed&0x80 != 0 {
				offset += 3 * (1 << ((packed & 7) + 1))
			}
			offset++
			if offset > len(data) || !skipBlocks() {
				return invalid
			}
		default:
			return invalid
		}
	}
	return invalid
}

func (d *richDocument) plainText() string {
	if d == nil {
		return ""
	}
	var text func(*richNode) string
	text = func(node *richNode) string {
		switch node.Type {
		case "text":
			return node.Text
		case "hardBreak":
			return "\n"
		case "mention":
			label, _ := richString(node.Attrs, "label")
			return "@" + label
		case "image", "attachment":
			name, _ := richString(node.Attrs, "name")
			alt, _ := richString(node.Attrs, "alt")
			if alt != "" {
				return alt
			}
			return name
		case "horizontalRule":
			return ""
		}
		parts := make([]string, 0, len(node.Content))
		for index, child := range node.Content {
			value := text(child)
			if index > 0 && (child.Type == "mention" || node.Content[index-1].Type == "mention") && len(parts[index-1]) > 0 && value != "" {
				last, _ := utf8.DecodeLastRuneInString(parts[index-1])
				first, _ := utf8.DecodeRuneInString(value)
				if child.Type == "mention" && mentionWordRune(last) || node.Content[index-1].Type == "mention" && mentionWordRune(first) {
					value = " " + value
				}
			}
			parts = append(parts, value)
		}
		separator := "\n"
		if node.Type == "paragraph" || node.Type == "heading" || node.Type == "codeBlock" {
			separator = ""
		}
		return strings.Join(parts, separator)
	}
	return text(d.root)
}
func (d *richDocument) mentionIDs() []string {
	ids := map[string]string{}
	for _, node := range d.mentions {
		id, _ := richString(node.Attrs, "id")
		ids[id] = ""
	}
	return mentionIDs(ids)
}

// 新提及按当前项目稳定成员 ID 校验；历史姓名快照仅用于保留旧绑定，不能据同名猜测新收件人。
func (a *App) validateRichMentions(tx *sql.Tx, d *richDocument, previous map[string]string) error {
	active, err := a.activeRequirementMemberNames(tx)
	if err != nil {
		return err
	}
	if len(d.mentionIDs()) > 50 {
		return richValidationError("富文本最多提及 50 位成员")
	}
	for _, node := range d.mentions {
		id, _ := richString(node.Attrs, "id")
		label, _ := richString(node.Attrs, "label")
		current, valid := active[id]
		if !(valid && label == current) && !(previous[id] != "" && label == previous[id]) {
			return richValidationError("富文本提及成员或姓名与当前项目不匹配")
		}
		if valid {
			node.Attrs["label"] = richRaw(current)
		}
	}
	if utf8.RuneCountInString(d.plainText()) > richMaxText {
		return richValidationError("富文本纯文本不能超过 100000 字")
	}
	return nil
}

// 兼容旧客户端：未改纯文本时保留已存文档和提及；显式改正文或提交 descriptionDoc:null 才切回纯文本。
func (a *App) prepareRichRequirement(tx *sql.Tx, x *Requirement, patch map[string]json.RawMessage, creating bool, doc *richDocument) error {
	var previousDescription string
	var previousDoc sql.NullString
	previous := map[string]string{}
	if !creating {
		var mentions string
		if err := tx.QueryRow(`SELECT description,description_doc_json,text_mentions_json FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), x.ID).Scan(&previousDescription, &previousDoc, &mentions); err != nil {
			return err
		}
		var state requirementMentionState
		if err := json.Unmarshal([]byte(mentions), &state); err != nil {
			return err
		}
		previous = state.Description
	}
	if doc != nil {
		if err := a.validateRichMentions(tx, doc, previous); err != nil {
			return err
		}
		x.Description = doc.plainText()
		x.DescriptionMentionUserIDs = doc.mentionIDs()
		return nil
	}
	if creating || hasPatch(patch, "descriptionDoc") {
		x.DescriptionDoc = nil
		return nil
	}
	if hasPatch(patch, "description") && x.Description != previousDescription {
		x.DescriptionDoc = nil
		return nil
	}
	var err error
	x.DescriptionDoc, err = readRichDocument(previousDoc)
	if err == nil && !richIsNull(x.DescriptionDoc) && hasPatch(patch, "descriptionMentionUserIds") {
		// An unchanged legacy text save must not silently remove the identities
		// attached to rich mention nodes. Changing the document/text is explicit.
		x.DescriptionMentionUserIDs = mentionIDs(previous)
		patch["descriptionMentionUserIds"] = richRaw(x.DescriptionMentionUserIDs)
	}
	return err
}

// 附件、文档及审计共用调用方事务；旧 attachmentId 必须属于同企业/项目/需求，新二进制成功后替换为 ID。
// 持久化 JSON 必须删除 data，避免正文、搜索、通知或审计携带 base64；这里不会抓取任何外部图片 URL。
func (a *App) persistRichDocument(tx *sql.Tx, doc *richDocument, requirementID int64, now string) (json.RawMessage, error) {
	if doc == nil {
		return nil, nil
	}
	for _, file := range doc.files {
		id := file.id
		name := file.name
		if file.pending {
			category, _ := richString(file.node.Attrs, "category")
			if category == "" {
				category = "auto"
			}
			digest := sha256.Sum256(file.content)
			if file.content == nil {
				file.content = []byte{}
			}
			item := requirementAttachment{RequirementID: requirementID, Name: name, Size: int64(len(file.content)), ContentType: http.DetectContentType(file.content), SHA256: hex.EncodeToString(digest[:]), CreatedBy: a.uid(), CreatedAt: now}
			res, err := tx.Exec(`INSERT INTO requirement_attachments(tenant_id,project_id,requirement_id,name,size_bytes,content_type,sha256,content,created_by,created_at,category)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, name, item.Size, item.ContentType, item.SHA256, file.content, a.uid(), now, category)
			if err != nil {
				return nil, err
			}
			id, err = res.LastInsertId()
			if err != nil {
				return nil, err
			}
			item.ID = id
			item.Category, item.Language = resolveAttachmentAsset(name, category, file.content)
			if err := a.auditRequirementResource(tx, requirementID, id, "requirement_attachment", "attachment.uploaded", nil, item, now); err != nil {
				return nil, err
			}
		} else {
			var content []byte
			if err := tx.QueryRow(`SELECT name,content FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&name, &content); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, richValidationError("富文本附件不属于当前需求或已被删除")
				}
				return nil, err
			}
			if !safeAttachmentFilename(name) {
				return nil, fmt.Errorf("stored attachment filename invalid")
			}
			if file.node.Type == "image" {
				if err := validateRichImage(content); err != nil {
					return nil, err
				}
			}
		}
		delete(file.node.Attrs, "data")
		// 分类以附件表为准，避免正文/多条评论缓存的分类相互覆盖。
		delete(file.node.Attrs, "category")
		file.node.Attrs["attachmentId"] = richRaw(id)
		file.node.Attrs["name"] = richRaw(name)
	}
	return richRaw(doc.root), nil
}

func richSQL(raw json.RawMessage) any {
	if richIsNull(raw) {
		return nil
	}
	return string(raw)
}
func (a *App) auditRichDocument(tx *sql.Tx, requirementID, commentID int64, raw json.RawMessage, now string) error {
	action := "requirement.description_saved"
	objectID := requirementID
	kind := "requirement"
	if commentID > 0 {
		action = "comment.rich_created"
		objectID = commentID
		kind = "comment"
	}
	_, err := tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,'null',?,?)`, tenantID, a.pid(), a.uid(), kind, strconv.FormatInt(objectID, 10), action, jsonText(map[string]any{"requirementId": requirementID, "document": raw}), now)
	return err
}

func (a *App) richAttachmentInUse(tx *sql.Tx, requirementID, attachmentID int64) (bool, error) {
	var inUse bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM requirements r,json_tree(r.description_doc_json) j WHERE r.tenant_id=? AND r.project_id=? AND r.id=? AND j.key='attachmentId' AND j.type='integer' AND j.atom=?) OR EXISTS(SELECT 1 FROM comments c,json_tree(c.content_doc_json) j WHERE c.tenant_id=? AND c.project_id=? AND c.requirement_id=? AND j.key='attachmentId' AND j.type='integer' AND j.atom=?)`, tenantID, a.pid(), requirementID, attachmentID, tenantID, a.pid(), requirementID, attachmentID).Scan(&inUse)
	return inUse, err
}
