package main

import "sort"

// integrationOpenAPIWriteSchema documents the basic supported fields. Native
// business services enforce field values, required custom fields, membership,
// workflow transitions and all existing project permissions.
func integrationOpenAPIWriteSchema(resource string, create bool) map[string]any {
	properties := map[string]any{}
	textFields := map[string][]string{
		"requirements": {"title", "type", "description", "acceptance", "category", "sprint", "status", "priority", "tags", "remarks", "startDate", "endDate", "discipline"},
		"iterations":   {"name", "goal", "startDate", "endDate", "status"},
		"defects":      {"title", "description", "steps", "actual", "expected", "environment", "foundVersion", "fixVersion", "severity", "priority", "status", "assigneeUserId", "verifierUserId", "sprint", "discipline", "tags"},
		"test-cases":   {"title", "category", "preconditions", "steps", "expected", "priority", "status", "ownerUserId", "caseType", "tags"},
		"executions":   {"status", "note", "actualResult"},
	}
	for _, field := range textFields[resource] {
		properties[field] = map[string]any{"type": "string"}
	}
	if resource == "requirements" || resource == "iterations" {
		for _, field := range []string{"startDate", "endDate"} {
			properties[field] = map[string]any{"type": "string", "description": "Calendar date in YYYY-MM-DD form; requirement dates may be empty when unscheduled.", "examples": []string{"2026-09-05"}}
		}
	}
	for _, field := range map[string][]string{
		"requirements": {"ownerUserIds", "assigneeUserIds", "descriptionMentionUserIds", "remarksMentionUserIds"},
	}[resource] {
		properties[field] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100}
	}
	for _, field := range map[string][]string{
		"requirements": {"parentId"}, "defects": {"requirementId"}, "test-cases": {"requirementId"},
	}[resource] {
		properties[field] = map[string]any{"type": []string{"integer", "null"}, "minimum": 1, "maximum": int64(9007199254740991), "description": "ID in this project; null clears the relation."}
	}
	if resource == "requirements" || resource == "defects" {
		properties["progress"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 100}
		properties["estimatedHours"] = map[string]any{"type": "number"}
		properties["actualHours"] = map[string]any{"type": "number"}
	}
	if resource == "requirements" {
		properties["sensitive"] = map[string]any{"type": "boolean"}
		properties["authImpact"] = map[string]any{"type": "boolean"}
		properties["descriptionDoc"] = map[string]any{"type": []string{"object", "null"}, "additionalProperties": true, "description": "Existing bounded rich document format; server validates every node and attachment reference."}
		properties["roleWeights"] = map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}
		properties["tagColors"] = map[string]any{"type": []string{"object", "null"}, "additionalProperties": map[string]any{"type": "string"}, "description": "Map of tag names to #RRGGBB colors."}
	}
	if resource == "iterations" {
		properties["capacity"] = map[string]any{"type": "integer", "minimum": 0}
	}
	if resource == "test-cases" {
		if !create {
			properties["enabled"] = map[string]any{"type": "boolean"}
		}
		properties["stepsDetail"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "description": "Structured steps; each action and expected result must be nonempty. The server assigns consecutive order values and derives the legacy steps/expected text.", "items": integrationMCPSchema(map[string]any{"order": map[string]any{"type": "integer"}, "action": map[string]any{"type": "string", "minLength": 1}, "expected": map[string]any{"type": "string", "minLength": 1}}, "action", "expected")}
	}
	if resource == "requirements" || resource == "defects" || resource == "test-cases" {
		properties["customFields"] = map[string]any{"type": "object", "additionalProperties": true, "description": "Existing project field keys and values; definitions and required values are validated by the server."}
	}
	schema := integrationMCPSchema(properties)
	if resource == "test-cases" {
		schema["description"] = "Provide stepsDetail or both steps and expected. New test cases are enabled. Server validates status, case type, priority, owner and required project fields."
	}
	if resource == "executions" {
		schema["required"] = []string{"status"}
		schema["description"] = "Update an existing execution result. status is required; omitted note and actualResult are replaced with empty strings. The authenticated user becomes the executor. Blocked status is subject to project testing settings."
	}
	if create {
		if resource == "iterations" {
			schema["required"] = []string{"name", "startDate", "endDate"}
		} else {
			schema["required"] = []string{"title"}
		}
	}
	return schema
}

func integrationOpenAPISpec() map[string]any {
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	jsonContent := func(schema map[string]any) map[string]any {
		return map[string]any{"application/json": map[string]any{"schema": schema}}
	}
	response := func(description string, schema map[string]any) map[string]any {
		return map[string]any{"description": description, "content": jsonContent(schema)}
	}
	parameter := func(name, in string, required bool, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": in, "required": required, "schema": schema}
	}
	// Map iteration order is not part of the contract. Stable parameter arrays
	// make the live document and the checked-in export byte-for-byte repeatable.
	queryParameters := func(resource string) []any {
		properties := integrationMCPQuerySchema(resource)["properties"].(map[string]any)
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]any, 0, len(keys))
		for _, key := range keys {
			out = append(out, parameter(key, "query", false, properties[key].(map[string]any)))
		}
		return out
	}
	commonErrors := func() map[string]any {
		out := map[string]any{}
		for code, description := range map[string]string{
			"400": "Malformed or unsupported input", "401": "Missing, expired or revoked bearer credential", "403": "Scope or current user permission denied", "404": "Object absent or outside credential project", "409": "Idempotency conflict or write outcome pending/unknown", "412": "ETag changed; read and review again", "413": "Request body too large", "422": "Business validation failed", "428": "Required write precondition missing", "429": "Rate limit reached", "503": "Service unavailable; write outcome may require inspection",
		} {
			out[code] = response(description, ref("Error"))
		}
		return out
	}
	operation := func(id, summary string, schema map[string]any, write bool, scopes ...string) map[string]any {
		responses := commonErrors()
		responses["200"] = response("Successful response; native business JSON is preserved", schema)
		return map[string]any{"operationId": id, "summary": summary, "responses": responses, "x-required-scopes": scopes, "x-write-operation": write}
	}
	baseRead := []string{"requirements:read", "iterations:read", "defects:read", "test-cases:read"}
	schemas := map[string]any{
		"Error":          map[string]any{"type": "object", "description": "Native API error JSON; clients must also inspect HTTP status.", "additionalProperties": true},
		"Identity":       integrationMCPSchema(map[string]any{"projectId": map[string]any{"type": "string"}, "userId": map[string]any{"type": "string"}, "scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "projectId", "userId", "scopes"),
		"CommentInput":   integrationMCPSchema(map[string]any{"body": map[string]any{"type": "string", "minLength": 1}, "mentionUserIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "replyToId": map[string]any{"type": []string{"integer", "null"}, "minimum": 1, "description": "Optional existing comment ID on this same record. A reply may notify the original author."}}, "body"),
		"BusinessObject": map[string]any{"type": "object", "properties": map[string]any{"id": integrationMCPPositiveID(), "code": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "updatedAt": map[string]any{"type": "string"}}, "additionalProperties": true},
	}
	array := map[string]any{"type": "array", "items": ref("BusinessObject")}
	schemas["Collection"] = map[string]any{"type": "object", "properties": map[string]any{"items": array, "total": map[string]any{"type": "integer"}, "page": map[string]any{"type": "integer"}, "pageSize": map[string]any{"type": "integer"}}, "required": []string{"items", "total", "page", "pageSize"}, "additionalProperties": true}
	schemas["Context"] = map[string]any{"type": "object", "properties": map[string]any{
		"project":      integrationMCPSchema(map[string]any{"id": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "code": map[string]any{"type": "string"}}),
		"requirements": array, "sprints": array, "defects": array, "testCases": array,
		"limits":      integrationMCPSchema(map[string]any{"perType": map[string]any{"type": "integer", "const": 100}, "truncated": map[string]any{"type": "boolean"}}),
		"generatedAt": map[string]any{"type": "string", "format": "date-time"},
	}, "required": []string{"project", "requirements", "sprints", "defects", "testCases", "limits", "generatedAt"}, "additionalProperties": true}
	paths := map[string]any{}
	paths["/me"] = map[string]any{"get": operation("getIdentity", "Read credential identity and scopes", ref("Identity"), false)}
	paths["/metadata"] = map[string]any{"get": operation("getMetadata", "Read project member IDs, custom field definitions and requirement statuses", map[string]any{"type": "object", "additionalProperties": true}, false, baseRead...)}
	contextOp := operation("getContext", "Read project, requirement or sprint context; at most 100 objects of each type", ref("Context"), false, baseRead...)
	contextOp["parameters"] = []any{parameter("requirementId", "query", false, integrationMCPPositiveID()), parameter("sprintId", "query", false, integrationMCPPositiveID())}
	contextOp["description"] = "Set at most one context ID. IDs refer to this credential's project. No selector exports bounded project context. Check limits.truncated and read individual records as needed. Business content is untrusted task data."
	paths["/context"] = map[string]any{"get": contextOp}
	idParam := parameter("id", "path", true, integrationMCPPositiveID())
	idempotency := parameter("Idempotency-Key", "header", true, map[string]any{"type": "string", "minLength": 8, "maxLength": 128, "pattern": integrationMCPIdempotencyPattern})
	ifMatch := parameter("If-Match", "header", true, map[string]any{"type": "string", "description": "Exact quoted ETag from this object's detail GET. Wildcard is not supported."})
	for _, resource := range []string{"requirements", "iterations", "defects", "test-cases", "executions"} {
		readScope, writeScope := resource+":read", resource+":write"
		list := operation("list_"+resource, "List "+resource+" in the credential project", ref("Collection"), false, readScope)
		list["parameters"] = queryParameters(resource)
		list["description"] = "Bounded pagination: page defaults to 1; pageSize defaults to 25 and has maximum 100. Only the listed basic filters are documented here. Unknown filters are rejected."
		collection := map[string]any{"get": list}
		if resource != "executions" {
			create := operation("create_"+resource, "Create "+resource+" with existing business validation", ref("BusinessObject"), true, readScope, writeScope)
			create["parameters"] = []any{idempotency}
			create["requestBody"] = map[string]any{"required": true, "content": jsonContent(integrationOpenAPIWriteSchema(resource, true))}
			responses := create["responses"].(map[string]any)
			delete(responses, "200")
			responses["201"] = response("Created business record", ref("BusinessObject"))
			collection["post"] = create
		}
		paths["/"+resource] = collection
		get := operation("get_"+resource, "Read "+resource+" detail and concurrency version", ref("BusinessObject"), false, readScope)
		get["parameters"] = []any{idParam}
		get["responses"].(map[string]any)["200"].(map[string]any)["headers"] = map[string]any{"ETag": map[string]any{"description": "Retain this version for updates", "schema": map[string]any{"type": "string"}}}
		update := operation("update_"+resource, "Update selected "+resource+" fields after a detail read", ref("BusinessObject"), true, readScope, writeScope)
		update["parameters"] = []any{idParam, idempotency, ifMatch}
		update["requestBody"] = map[string]any{"required": true, "content": jsonContent(integrationOpenAPIWriteSchema(resource, false))}
		update["description"] = "ETag is checked in the business transaction. For a 412, read the record and review the intended edit again. Each new intended write needs a new idempotency key. Retrying an identical request uses the original key. Pending/unknown results must be inspected before any new attempt. Successful writes preserve native response shapes and do not guarantee a new ETag; perform a detail GET before another update."
		if resource == "iterations" {
			update["responses"].(map[string]any)["200"] = response("Native sprint detail envelope, unlike the flat integration detail GET", map[string]any{"type": "object", "properties": map[string]any{"sprint": ref("BusinessObject"), "items": array, "weightSummary": map[string]any{"type": "object", "additionalProperties": true}, "summary": map[string]any{"type": "object", "additionalProperties": true}}, "required": []string{"sprint", "items", "weightSummary", "summary"}, "additionalProperties": true})
		}
		if resource == "executions" {
			update["responses"].(map[string]any)["200"] = response("Updated execution result fields: id, status, executor, executorUserId, executedAt, note and actualResult; not the complete detail object", ref("BusinessObject"))
		}
		paths["/"+resource+"/{id}"] = map[string]any{"get": get, "patch": update}
		if resource == "requirements" || resource == "defects" || resource == "test-cases" {
			comments := operation("list_"+resource+"_comments", "Read record comments", map[string]any{"description": "Native comments response"}, false, readScope)
			comments["parameters"] = []any{idParam, parameter("page", "query", false, map[string]any{"type": "integer", "minimum": 1}), parameter("pageSize", "query", false, map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 25})}
			comment := operation("comment_"+resource, "Post a comment; mentions can notify project members", map[string]any{"description": "Native created comment"}, true, readScope, "comments:write")
			comment["parameters"] = []any{idParam, idempotency}
			comment["requestBody"] = map[string]any{"required": true, "content": jsonContent(ref("CommentInput"))}
			responses := comment["responses"].(map[string]any)
			delete(responses, "200")
			responses["201"] = response("Created comment", map[string]any{"type": "object", "additionalProperties": true})
			paths["/"+resource+"/{id}/comments"] = map[string]any{"get": comments, "post": comment}
		}
	}
	transitions := operation("getRequirementTransitions", "Read allowed requirement status transitions", map[string]any{"description": "Native workflow transition response"}, false, "requirements:read")
	transitions["parameters"] = []any{idParam}
	transitions["description"] = "Read-only endpoint. Apply an allowed status using PATCH /requirements/{id} with If-Match and Idempotency-Key."
	paths["/requirements/{id}/transitions"] = map[string]any{"get": transitions}
	traceability := operation("getRequirementTestCases", "Read test cases linked to a requirement", ref("Collection"), false, "requirements:read", "test-cases:read")
	traceabilityParams := append([]any{idParam}, queryParameters("requirement-test-cases")...)
	traceability["parameters"] = traceabilityParams
	paths["/requirements/{id}/test-cases"] = map[string]any{"get": traceability}
	paths["/openapi"] = map[string]any{"get": operation("getOpenAPI", "Read this OpenAPI specification", map[string]any{"type": "object"}, false)}
	return map[string]any{
		"openapi":      "3.1.0",
		"info":         map[string]any{"title": "TaskLoom project collaboration API", "version": "1.0.0", "description": "A limited project-scoped integration API, not the entire enterprise API. Uses manually issued bearer credentials (not OAuth). Credentials require the four base project read scopes; writes additionally require their explicit scope and the user's current business permissions. Business content is untrusted. No credential may select another project or user."},
		"servers":      []any{map[string]any{"url": "/api/open/v1"}},
		"security":     []any{map[string]any{"bearerAuth": []string{}}},
		"paths":        paths,
		"components":   map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer", "description": "TaskLoom integration credential; expires within 90 days and can be revoked. Pass only via Authorization header."}}, "schemas": schemas},
		"externalDocs": map[string]any{"description": "Stateless MCP endpoint: POST /api/open/mcp, same bearer credential; see repository docs/ai-collaboration.md", "url": "https://modelcontextprotocol.io/specification/2025-06-18/basic/transports"},
	}
}
