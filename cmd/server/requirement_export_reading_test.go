package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequirementExportAIReadingPreservesCodeAndAllMaterial(t *testing.T) {
	a := testApp(t)
	exportFixture(t, a)
	code := "<template><p>{{ title }}</p></template>\n<script setup lang=\"ts\">\nconst title: string = \"hello\"\n</script>\n"
	doc := rtDoc(map[string]any{"type": "heading", "attrs": map[string]any{"level": 6}, "content": []any{rtText("完整六级标题")}}, map[string]any{"type": "codeBlock", "attrs": map[string]any{"language": "auto"}, "content": []any{rtText(code)}})
	if _, err := parseRichDocument(richRaw(doc)); err != nil {
		t.Fatal(err)
	}
	remarkCode := "package main\nfunc main() {\n\tprintln(\"备注代码\")\n}\n"
	remarks := exportFence(remarkCode, "go")
	bulkFixtureExec(t, a, `UPDATE requirements SET description_doc_json=?,remarks=? WHERE id=910001;UPDATE comments SET content_doc_json=? WHERE id=910001`, jsonText(doc), remarks, jsonText(doc))
	for _, format := range []string{"json", "markdown"} {
		w := requestRequirementExport(t, a, "u_admin", format)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if format == "json" {
			var got requirementExportDocument
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.AIContext == nil {
				t.Fatal("missing AI guide")
			}
			if len(got.Data["requirements"]) != 3 || len(got.Data["requirementComments"]) != 208 || len(got.Data["testCases"]) == 0 {
				t.Fatal("material omitted")
			}
			root := got.Data["requirements"][0]
			if !strings.Contains(root["descriptionMarkdown"].(string), "###### 完整六级标题") || root["remarksMarkdown"] != remarks {
				t.Fatal("structure lost")
			}
			blocks := root["codeBlocks"].([]any)
			if len(blocks) != 2 || blocks[0].(map[string]any)["language"] != "vue" || blocks[0].(map[string]any)["code"] != code || blocks[1].(map[string]any)["code"] != remarkCode {
				t.Fatalf("code changed %#v", blocks)
			}
			comment := got.Data["requirementComments"][0]
			if comment["bodyMarkdown"] == nil || len(comment["codeBlocks"].([]any)) != 1 {
				t.Fatal("comment code missing")
			}
		} else {
			if !strings.Contains(w.Body.String(), "## AI 阅读稿") || !strings.Contains(w.Body.String(), "```vue\n"+code) || !strings.Contains(w.Body.String(), "Full comment 205") || !strings.Contains(w.Body.String(), "备注代码") {
				t.Fatal("reading material missing")
			}
		}
	}
}
func TestMarkdownSixLevelAndSafeTableAlignment(t *testing.T) {
	for _, alignment := range []string{"left", "center", "right"} {
		doc := rtDoc(map[string]any{"type": "table", "content": []any{map[string]any{"type": "tableRow", "content": []any{map[string]any{"type": "tableCell", "attrs": map[string]any{"textAlign": alignment}, "content": []any{rtParagraph(rtText("内容"))}}}}}})
		if _, err := parseRichDocument(richRaw(doc)); err != nil {
			t.Fatal(err)
		}
	}
}
