package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This is a test credential, not a key loaded from the environment or database.
const aiQualityCredential = "SYNTHETIC_EVAL_CREDENTIAL_DO_NOT_RETURN"

var aiQualityCandidatePath = flag.String("ai-quality-candidate", "", "offline JSON candidate to evaluate against a synthetic AI quality fixture; never calls a real provider")

type aiQualityCheck struct {
	ID       string   `json:"id"`
	Field    string   `json:"field"`
	Contains []string `json:"contains"`
	Forbids  []string `json:"forbids"`
}

type aiQualityFixture struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Source     struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Acceptance  string `json:"acceptance"`
	} `json:"source"`
	PrivateContext  string           `json:"privateContext"`
	UniqueScenarios bool             `json:"uniqueScenarios"`
	Checks          []aiQualityCheck `json:"checks"`
	ManualReview    []string         `json:"manualReview"`
	Reference       json.RawMessage  `json:"reference"`
	Counterexamples []struct {
		ID               string          `json:"id"`
		Output           json.RawMessage `json:"output"`
		ExpectedFailures []string        `json:"expectedFailures"`
		ProviderBehavior string          `json:"providerBehavior"`
	} `json:"counterexamples"`
}

func readAIQualityJSON(t *testing.T, path string, target any) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		t.Fatal("quality input must be an accessible regular JSON file of at most 1 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil || strictAIJSON(raw, target) != nil {
		t.Fatal("quality input must match the documented JSON format")
	}
}

func loadAIQualityFixtures(t *testing.T) []aiQualityFixture {
	t.Helper()
	var catalog struct {
		SchemaVersion int                `json:"schemaVersion"`
		Provenance    string             `json:"provenance"`
		Fixtures      []aiQualityFixture `json:"fixtures"`
	}
	readAIQualityJSON(t, "testdata/ai_quality/fixtures.json", &catalog)
	if catalog.SchemaVersion != 1 || !strings.Contains(catalog.Provenance, "Synthetic") {
		t.Fatal("fixture schema/provenance is missing")
	}
	seen := map[string]bool{}
	for _, fixture := range catalog.Fixtures {
		if fixture.ID == "" || seen[fixture.ID] || !validChoice(fixture.Capability, []string{"title", "refinement", "test-cases"}) || fixture.Source.Description == "" || len(fixture.Checks) == 0 || len(fixture.ManualReview) == 0 || len(fixture.Counterexamples) == 0 || !json.Valid(fixture.Reference) {
			t.Fatalf("incomplete or duplicate quality fixture %q", fixture.ID)
		}
		seen[fixture.ID] = true
		checkIDs := map[string]bool{}
		for _, check := range fixture.Checks {
			if check.ID == "" || checkIDs[check.ID] || check.Field == "" || len(check.Contains)+len(check.Forbids) == 0 {
				t.Fatalf("fixture %s has an empty or duplicate check", fixture.ID)
			}
			checkIDs[check.ID] = true
		}
		if fixture.UniqueScenarios {
			checkIDs["duplicate_scenarios"] = true
		}
		badIDs := map[string]bool{}
		for _, bad := range fixture.Counterexamples {
			if bad.ID == "" || badIDs[bad.ID] || !json.Valid(bad.Output) || len(bad.ExpectedFailures) == 0 || !validChoice(bad.ProviderBehavior, []string{"accepted_for_review", "rejected", "deduplicated"}) {
				t.Fatalf("fixture %s has an invalid counterexample", fixture.ID)
			}
			badIDs[bad.ID] = true
			for _, code := range bad.ExpectedFailures {
				if !checkIDs[code] {
					t.Fatalf("fixture %s counterexample references unknown check %q", fixture.ID, code)
				}
			}
		}
	}
	for _, required := range []string{"title_preserves_scope", "unknown_batch_rules", "observable_search_acceptance", "distinct_form_test_cases", "injection_is_not_authority", "no_imagined_screenshot_evidence"} {
		if !seen[required] {
			t.Fatalf("required evaluation scenario %s was removed", required)
		}
	}
	return catalog.Fixtures
}

func aiQualityText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []any:
		parts := []string{}
		for _, item := range value {
			parts = append(parts, aiQualityText(item))
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, key := range keys {
			parts = append(parts, aiQualityText(value[key]))
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func aiQualityDescription(fixture aiQualityFixture) string {
	if fixture.Capability == "refinement" {
		// Match RequirementRefinement's explicit description/acceptance input.
		return fixture.Source.Description + "\n\n已有验收标准：\n" + fixture.Source.Acceptance
	}
	return fixture.Source.Description
}

// These are deliberately narrow fixture checks, not a general factuality judge.
// Only known fragments and exact normalized scenario duplicates are detectable.
func evaluateAIQuality(fixture aiQualityFixture, raw json.RawMessage) []string {
	var output map[string]any
	if json.Unmarshal(raw, &output) != nil || output == nil {
		return []string{"output_json"}
	}
	failures := []string{}
	for _, check := range fixture.Checks {
		text := aiQualityText(output[check.Field])
		if check.Field == "*" {
			text = aiQualityText(output)
		}
		failed := false
		for _, fragment := range check.Contains {
			failed = failed || !strings.Contains(text, fragment)
		}
		for _, fragment := range check.Forbids {
			failed = failed || strings.Contains(text, fragment)
		}
		if failed {
			failures = append(failures, check.ID)
		}
	}
	if fixture.UniqueScenarios {
		seen := map[string]bool{}
		cases, _ := output["cases"].([]any)
		for _, item := range cases {
			item, _ := item.(map[string]any)
			scenario := []string{strings.Join(strings.Fields(aiQualityText(item["preconditions"])), " ")}
			steps, _ := item["stepsDetail"].([]any)
			for _, step := range steps {
				step, _ := step.(map[string]any)
				scenario = append(scenario, strings.Join(strings.Fields(aiQualityText(step["action"])), " "), strings.Join(strings.Fields(aiQualityText(step["expected"])), " "))
			}
			fingerprint := jsonText(scenario)
			if seen[fingerprint] {
				failures = append(failures, "duplicate_scenarios")
				break
			}
			seen[fingerprint] = true
		}
	}
	sort.Strings(failures)
	return failures
}

// Replay through the real request builder and output validator using an entirely
// synthetic HTTP transport. There is no route to an external model in this test.
func replayAIQualityProvider(t *testing.T, fixture aiQualityFixture, output json.RawMessage) (json.RawMessage, error) {
	t.Helper()
	a := &App{}
	calls := 0
	aiMock(a, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != aiEndpoint || request.Header.Get("Authorization") != "Bearer "+aiQualityCredential {
			t.Fatal("mock provider received an unexpected route or credential")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal("invalid provider request")
		}
		instructions, _ := payload["instructions"].(string)
		for _, required := range []string{"untrusted", "no tools", "待确认", "test evidence", "screenshot"} {
			if !strings.Contains(strings.ToLower(instructions), required) {
				t.Fatalf("prompt omitted boundary %q", required)
			}
		}
		if fixture.Capability != "title" && !strings.Contains(instructions, "observable expected result") {
			t.Fatal("prompt omitted observable acceptance guidance")
		}
		if payload["store"] != false || payload["tools"] != nil || strings.Contains(instructions, "EVAL_UNTRUSTED_INSTRUCTION") || strings.Contains(jsonText(payload), aiQualityCredential) {
			t.Fatal("request expanded authority, exposed a credential or promoted source instructions")
		}
		if fixture.PrivateContext != "" && strings.Contains(jsonText(payload), fixture.PrivateContext) {
			t.Fatal("private metadata was included in exported input")
		}
		var input map[string]string
		inputJSON, _ := payload["input"].(string)
		if json.Unmarshal([]byte(inputJSON), &input) != nil {
			t.Fatal("provider input is not structured source data")
		}
		want := map[string]string{"description": aiQualityDescription(fixture)}
		if fixture.Capability == "test-cases" {
			want["title"], want["acceptance"] = fixture.Source.Title, fixture.Source.Acceptance
		}
		if !reflect.DeepEqual(input, want) {
			t.Fatal("provider source differs from the explicitly selected fields")
		}
		return aiResponse(string(output)), nil
	})
	defer func() {
		if calls != 1 {
			t.Errorf("expected exactly one mock provider request, got %d", calls)
		}
	}()
	if fixture.Capability == "test-cases" {
		cases, err := a.generateAITestCases(context.Background(), aiQualityCredential, "gpt-5-mini", Requirement{Title: fixture.Source.Title, Description: fixture.Source.Description, Acceptance: fixture.Source.Acceptance, Remarks: fixture.PrivateContext})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(jsonText(map[string]any{"cases": cases})), nil
	}
	text, err := a.generateAIRequirementText(context.Background(), aiQualityCredential, "gpt-5-mini", aiDefaultBaseURL, aiQualityDescription(fixture), fixture.Capability == "refinement")
	if err != nil {
		return nil, err
	}
	if fixture.Capability == "refinement" {
		return json.RawMessage(text), nil
	}
	return json.RawMessage(jsonText(map[string]any{"title": text, "insufficient": false})), nil
}

func TestAIQualityEvaluationReferences(t *testing.T) {
	for _, fixture := range loadAIQualityFixtures(t) {
		t.Run(fixture.ID, func(t *testing.T) {
			if failures := evaluateAIQuality(fixture, fixture.Reference); len(failures) != 0 {
				t.Fatalf("hand-written reference failed fixture rules: %v", failures)
			}
			result, err := replayAIQualityProvider(t, fixture, fixture.Reference)
			if err != nil {
				t.Fatalf("reference rejected by production output contract: %v", err)
			}
			if failures := evaluateAIQuality(fixture, result); len(failures) != 0 {
				t.Fatalf("processed reference failed fixture rules: %v", failures)
			}
			t.Logf("SYNTHETIC REFERENCE: local checks PASS; request/output boundaries PASS; human review REQUIRED: %s", strings.Join(fixture.ManualReview, " / "))
		})
	}
}

func TestAIQualityEvaluationCounterexamples(t *testing.T) {
	for _, fixture := range loadAIQualityFixtures(t) {
		for _, bad := range fixture.Counterexamples {
			t.Run(fixture.ID+"/"+bad.ID, func(t *testing.T) {
				failures := evaluateAIQuality(fixture, bad.Output)
				want := append([]string(nil), bad.ExpectedFailures...)
				sort.Strings(want)
				if !reflect.DeepEqual(failures, want) {
					t.Fatalf("counterexample detection changed: got %v, expected %v", failures, want)
				}
				processed, err := replayAIQualityProvider(t, fixture, bad.Output)
				switch bad.ProviderBehavior {
				case "rejected":
					if err == nil {
						t.Fatal("unsafe output was not rejected by the production validator")
					}
				case "deduplicated":
					if err != nil || len(evaluateAIQuality(fixture, processed)) != 0 {
						t.Fatal("production duplicate normalization failed")
					}
				case "accepted_for_review":
					if err != nil || !reflect.DeepEqual(evaluateAIQuality(fixture, processed), want) {
						t.Fatal("semantic counterexample no longer demonstrates the documented validator boundary")
					}
				}
				t.Logf("EXPECTED COUNTEREXAMPLE: detected %v; production behavior=%s; this is not a real-model score", failures, bad.ProviderBehavior)
			})
		}
	}
}

func TestAIQualityEvaluationAuthorization(t *testing.T) {
	var fixture aiQualityFixture
	for _, item := range loadAIQualityFixtures(t) {
		if item.ID == "injection_is_not_authority" {
			fixture = item
		}
	}
	a := aiApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"title": fixture.Source.Title, "description": fixture.Source.Description, "acceptance": fixture.Source.Acceptance})
	calls := 0
	aiMock(a, func(*http.Request) (*http.Response, error) {
		calls++
		return aiResponse(string(fixture.Reference)), nil
	})
	for _, capability := range []string{"title", "refinement", "test-cases"} {
		for _, scenario := range []struct {
			name, user, project string
			confirmed           bool
			want                int
		}{{"viewer", "u_viewer", projectID, true, 403}, {"missing_consent", "u_front", projectID, false, 422}, {"other_project", "u_admin", insightProjectID, true, 404}} {
			t.Run(capability+"/"+scenario.name, func(t *testing.T) {
				path := "/api/ai/requirement-title"
				body := map[string]any{"description": fixture.Source.Description, "requirementId": x.ID, "confirmed": scenario.confirmed}
				if capability == "refinement" {
					path = "/api/ai/requirement-refine"
				} else if capability == "test-cases" {
					path = fmt.Sprintf("/api/requirements/%d/ai-test-cases", x.ID)
					body = map[string]any{"requirementUpdatedAt": x.UpdatedAt, "confirmed": scenario.confirmed}
				}
				response := apiRequest(a, "POST", path, scenario.user, scenario.project, jsonText(body))
				if response.Code != scenario.want || calls != 0 {
					t.Fatalf("source injection changed authorization: status=%d, expected=%d, mock provider calls=%d", response.Code, scenario.want, calls)
				}
				t.Log("APPLICATION BOUNDARY: denied before provider call; source text did not grant authority")
			})
		}
	}
	if tableCount(t, a, "ai_title_requests") != 0 || tableCount(t, a, "ai_test_case_drafts") != 0 {
		t.Fatal("unauthorized injection fixtures consumed generation reservations")
	}
}

func TestAIQualityEvaluationCandidate(t *testing.T) {
	if *aiQualityCandidatePath == "" {
		t.Skip("No saved candidate supplied. Real-model quality is NOT evaluated; use -args -ai-quality-candidate=/absolute/path/candidate.json for offline replay.")
	}
	var candidate struct {
		SchemaVersion int             `json:"schemaVersion"`
		FixtureID     string          `json:"fixtureId"`
		Model         string          `json:"model"`
		PromptVersion string          `json:"promptVersion"`
		Output        json.RawMessage `json:"output"`
	}
	readAIQualityJSON(t, *aiQualityCandidatePath, &candidate)
	if candidate.SchemaVersion != 1 || candidate.Model == "" || candidate.PromptVersion == "" || !json.Valid(candidate.Output) {
		t.Fatal("candidate needs schemaVersion=1, fixtureId, declared model/promptVersion and a structured output object")
	}
	for _, fixture := range loadAIQualityFixtures(t) {
		if fixture.ID != candidate.FixtureID {
			continue
		}
		t.Logf("OFFLINE CANDIDATE: fixture=%s; declared model=%q; declared prompt=%q; metadata is not independently verified; current prompt=%q", fixture.ID, candidate.Model, candidate.PromptVersion, aiGenerationPromptVersion)
		if candidate.PromptVersion != aiGenerationPromptVersion {
			t.Log("PROMPT VERSION DIFFERS: compare under the captured prompt separately; this replay checks the current application contract only")
		}
		if failures := evaluateAIQuality(fixture, candidate.Output); len(failures) != 0 {
			t.Errorf("candidate local checks FAIL: %v", failures)
		}
		processed, err := replayAIQualityProvider(t, fixture, candidate.Output)
		if err != nil {
			t.Error("candidate production output contract FAIL (output body is not logged)")
		} else if failures := evaluateAIQuality(fixture, processed); len(failures) != 0 {
			t.Errorf("processed candidate local checks FAIL: %v", failures)
		}
		t.Logf("HUMAN REVIEW REQUIRED: %s. Passing local checks is not a model-quality approval.", strings.Join(fixture.ManualReview, " / "))
		return
	}
	t.Fatal("candidate fixtureId does not identify a known synthetic fixture")
}
