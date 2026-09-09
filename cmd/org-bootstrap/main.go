// org-bootstrap imports a reviewed organization manifest without starting the
// application, migrating its database, changing passwords, or sending messages.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const manifestLimit = 1024 * 1024

type bootstrapOptions struct {
	DB, Tenant, Project, Input string
	Apply                      bool
}
type bootstrapManifest struct {
	Version        int                   `json:"version"`
	Administrators []string              `json:"administrators"`
	Departments    []bootstrapDepartment `json:"departments"`
	Members        []bootstrapMember     `json:"members"`
}
type bootstrapDepartment struct {
	Key        string `json:"key"`
	ExistingID string `json:"existingId,omitempty"`
	Name       string `json:"name"`
	ParentKey  string `json:"parentKey,omitempty"`
	SortOrder  int    `json:"sortOrder"`
}
type bootstrapMember struct {
	Name                 string   `json:"name"`
	Email                string   `json:"email"`
	ExpectedName         string   `json:"expectedName,omitempty"`
	ExistingUserID       string   `json:"existingUserId,omitempty"`
	DepartmentKeys       []string `json:"departmentKeys"`
	PrimaryDepartmentKey string   `json:"primaryDepartmentKey"`
	ProjectRole          string   `json:"projectRole"`
}
type departmentState struct {
	ID, Name, Code, ParentID, Status, Source, ExternalID string
	SortOrder                                            int
}
type memberState struct {
	ID, Name, Email, Department, TenantRole, TenantStatus, ProjectRole, LegacyRole string
	Active, OperationDisabled                                                      bool
	Departments                                                                    []memberDepartment
}
type memberDepartment struct {
	ID, Status string
	Primary    bool
}
type bootstrapState struct {
	Departments []departmentState
	Members     []memberState
}
type departmentChange struct {
	Key    string           `json:"key"`
	Action string           `json:"action"`
	Before *departmentState `json:"before,omitempty"`
	After  departmentState  `json:"after"`
}
type memberChange struct {
	Action string       `json:"action"`
	Before *memberState `json:"before,omitempty"`
	After  memberState  `json:"after"`
}
type bootstrapPlan struct {
	Mode         string             `json:"mode"`
	TenantID     string             `json:"tenantId"`
	ProjectID    string             `json:"projectId"`
	ManifestHash string             `json:"manifestHash"`
	Departments  []departmentChange `json:"departments"`
	Members      []memberChange     `json:"members"`
	Warnings     []string           `json:"warnings"`
	Changes      int                `json:"changes"`
	stateHash    string
}

func main() {
	var o bootstrapOptions
	flag.StringVar(&o.DB, "db", "", "absolute path to an existing, migrated TaskLoom SQLite database")
	flag.StringVar(&o.Tenant, "tenant", "", "exact existing tenant ID")
	flag.StringVar(&o.Project, "project", "", "exact existing active project ID")
	flag.StringVar(&o.Input, "input", "", "reviewed JSON manifest file, or - for stdin")
	flag.BoolVar(&o.Apply, "apply", false, "explicitly apply the entire manifest transactionally; default is read-only preview")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Unexpected positional arguments")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	plan, err := runBootstrap(ctx, o, os.Stdin, bcrypt.DefaultCost+2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Organization import rejected:", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot output import result")
		os.Exit(1)
	}
}

func decodeBootstrap(input io.Reader) (bootstrapManifest, error) {
	var m bootstrapManifest
	raw, err := io.ReadAll(io.LimitReader(input, manifestLimit+1))
	if err != nil || len(raw) > manifestLimit || !utf8.Valid(raw) {
		return m, errors.New("manifest must be valid UTF-8 JSON, at most 1 MiB")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	if err := uniqueJSONKeys(json.NewDecoder(strings.NewReader(string(raw))), 0); err != nil {
		return m, errors.New("manifest contains invalid, deeply nested or duplicate JSON keys")
	}
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, errors.New("invalid manifest structure or unknown fields")
	}
	if d.Decode(new(any)) != io.EOF {
		return m, errors.New("manifest must contain exactly one JSON object")
	}
	return m, validateManifest(&m)
}

func uniqueJSONKeys(d *json.Decoder, depth int) error {
	if depth > 12 {
		return errors.New("depth")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("delimiter")
	}
	seen := map[string]bool{}
	for d.More() {
		if delimiter == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[strings.ToLower(name)] {
				return errors.New("duplicate key")
			}
			seen[strings.ToLower(name)] = true
		}
		if err := uniqueJSONKeys(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

var bootstrapKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,47}$`)
var bootstrapRoles = map[string]bool{"project_admin": true, "product": true, "frontend": true, "backend": true, "algorithm": true, "ui": true, "frontend_lead": true, "backend_lead": true, "qa": true, "viewer": true}

func validBootstrapText(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) < minimum || utf8.RuneCountInString(value) > maximum {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}
func bootstrapEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 || !strings.Contains(value, "@") || strings.ContainsAny(value, "\r\n\x00 ") {
		return "", errors.New("invalid full login email")
	}
	return value, nil
}
func validateManifest(m *bootstrapManifest) error {
	if m.Version != 1 || len(m.Members) == 0 || len(m.Members) > 500 || len(m.Departments) > 100 || len(m.Administrators) != 2 {
		return errors.New("version 1 requires 1–500 members, at most 100 departments, and exactly two explicit administrator emails")
	}
	admins := map[string]bool{}
	for i, raw := range m.Administrators {
		email, err := bootstrapEmail(raw)
		if err != nil || admins[email] {
			return errors.New("administrator emails must be valid and distinct")
		}
		m.Administrators[i], admins[email] = email, true
	}
	departments, existingIDs, caseKeys := map[string]bootstrapDepartment{}, map[string]bool{}, map[string]bool{}
	for _, d := range m.Departments {
		if !bootstrapKey.MatchString(d.Key) || !validBootstrapText(d.Name, 1, 80) || d.SortOrder < 0 || d.SortOrder > 100000 || (d.ExistingID != "" && !validBootstrapText(d.ExistingID, 1, 128)) {
			return errors.New("department key, name, existing ID or sort order is invalid")
		}
		if _, exists := departments[d.Key]; exists || caseKeys[strings.ToLower(d.Key)] || (d.ExistingID != "" && existingIDs[d.ExistingID]) {
			return errors.New("duplicate department key or existing ID")
		}
		departments[d.Key] = d
		caseKeys[strings.ToLower(d.Key)] = true
		existingIDs[d.ExistingID] = true
	}
	for _, d := range m.Departments {
		seen := map[string]bool{d.Key: true}
		for parent := d.ParentKey; parent != ""; {
			p, ok := departments[parent]
			if !ok || seen[parent] {
				return errors.New("department parent is missing or cyclic")
			}
			seen[parent], parent = true, p.ParentKey
		}
	}
	emails, names, userIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range m.Members {
		p := &m.Members[i]
		email, err := bootstrapEmail(p.Email)
		if err != nil || emails[email] || !validBootstrapText(p.Name, 1, 80) || names[p.Name] || !bootstrapRoles[p.ProjectRole] || (p.ProjectRole == "project_admin" && !admins[email]) {
			return fmt.Errorf("member %d has an invalid/duplicate name or email, or an unauthorized project role", i+1)
		}
		if p.ExpectedName != "" && !validBootstrapText(p.ExpectedName, 1, 80) || p.ExistingUserID != "" && (!validBootstrapText(p.ExistingUserID, 1, 128) || userIDs[p.ExistingUserID]) {
			return fmt.Errorf("member %d has an invalid expected identity", i+1)
		}
		p.Email, emails[email], names[p.Name], userIDs[p.ExistingUserID] = email, true, true, true
		deps := map[string]bool{}
		for _, key := range p.DepartmentKeys {
			if _, exists := departments[key]; !exists || deps[key] {
				return fmt.Errorf("member %d has a missing or duplicate department", i+1)
			}
			deps[key] = true
		}
		if (len(deps) > 0 && !deps[p.PrimaryDepartmentKey]) || (len(deps) == 0 && p.PrimaryDepartmentKey != "") {
			return fmt.Errorf("member %d must explicitly choose one of their departments as primary", i+1)
		}
	}
	for email := range admins {
		if !emails[email] {
			return errors.New("both administrator emails must appear in the member list")
		}
	}
	return nil
}

func regularExistingFile(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("database path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("database must be an existing non-empty regular file, not a symbolic link")
	}
	return nil
}
func openBootstrapDB(o bootstrapOptions, apply bool) (*sql.DB, error) {
	params := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(ON)"}}
	if apply {
		params.Set("mode", "rw")
		params.Add("_pragma", "synchronous(FULL)")
		params.Set("_txlock", "immediate")
	} else {
		params.Add("_pragma", "query_only(ON)")
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(o.DB), RawQuery: params.Encode()}).String())
	if err != nil {
		return nil, errors.New("cannot open database")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func runBootstrap(ctx context.Context, o bootstrapOptions, stdin io.Reader, cost int) (bootstrapPlan, error) {
	var empty bootstrapPlan
	if !validBootstrapText(o.Tenant, 1, 128) || !validBootstrapText(o.Project, 1, 128) || o.Input == "" {
		return empty, errors.New("explicit tenant, project and input are required")
	}
	if err := regularExistingFile(o.DB); err != nil {
		return empty, err
	}
	input := stdin
	if o.Input != "-" {
		info, err := os.Lstat(o.Input)
		if err != nil || !info.Mode().IsRegular() || info.Size() > manifestLimit {
			return empty, errors.New("manifest must be a regular file of at most 1 MiB")
		}
		file, err := os.Open(o.Input)
		if err != nil {
			return empty, errors.New("cannot open reviewed manifest")
		}
		defer file.Close()
		input = file
	}
	m, err := decodeBootstrap(input)
	if err != nil {
		return empty, err
	}
	ro, err := openBootstrapDB(o, false)
	if err != nil {
		return empty, err
	}
	defer ro.Close()
	tx, err := ro.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return empty, errors.New("cannot begin read-only organization preview")
	}
	plan, err := planBootstrap(ctx, tx, o, m)
	tx.Rollback()
	if err != nil || !o.Apply || plan.Changes == 0 {
		return plan, err
	}
	// Slow password work is outside the writer transaction. Random secrets are
	// never returned, logged, reused, or made usable by this import.
	hashes := map[string]string{}
	for _, p := range plan.Members {
		if ctx.Err() != nil {
			return empty, errors.New("organization preparation cancelled; no changes made")
		}
		if p.Action != "create" {
			continue
		}
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return empty, errors.New("cannot generate an inaccessible initial credential")
		}
		password := []byte(base64.RawURLEncoding.EncodeToString(secret[:]))
		hash, err := bcrypt.GenerateFromPassword(password, cost)
		clear(secret[:])
		clear(password)
		if err != nil {
			return empty, errors.New("cannot hash inaccessible initial credential")
		}
		hashes[p.After.ID] = string(hash)
		clear(hash)
	}
	return applyPreparedBootstrap(ctx, o, m, plan, hashes)
}

func applyPreparedBootstrap(ctx context.Context, o bootstrapOptions, m bootstrapManifest, plan bootstrapPlan, hashes map[string]string) (bootstrapPlan, error) {
	var empty bootstrapPlan
	rw, err := openBootstrapDB(o, true)
	if err != nil {
		return empty, err
	}
	defer rw.Close()
	write, err := rw.BeginTx(ctx, nil)
	if err != nil {
		return empty, errors.New("cannot reserve organization writer")
	}
	defer write.Rollback()
	current, err := planBootstrap(ctx, write, o, m)
	if err != nil {
		return empty, err
	}
	if current.stateHash != plan.stateHash {
		return empty, errors.New("organization changed during preparation; rerun the preview")
	}
	if err = applyBootstrap(ctx, write, o, current, hashes); err != nil {
		return empty, err
	}
	if err = write.Commit(); err != nil {
		return empty, errors.New("organization transaction could not commit")
	}
	current.Mode = "applied"
	return current, nil
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func bootstrapID(prefix, tenant, key string) string {
	sum := sha256.Sum256([]byte(tenant + "\x00" + key))
	return prefix + hex.EncodeToString(sum[:16])
}

func readBootstrapState(ctx context.Context, tx *sql.Tx, o bootstrapOptions) (bootstrapState, error) {
	s := bootstrapState{Departments: []departmentState{}, Members: []memberState{}}
	for _, query := range []string{
		`SELECT password_hash,must_change_password,password_changed_at,directory_source,directory_external_id,employee_no,last_active FROM users LIMIT 0`,
		`SELECT tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at FROM audit_logs LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return s, errors.New("database is missing required migrated schema; this tool never migrates or seeds")
		}
		if err = rows.Close(); err != nil {
			return s, errors.New("schema verification failed")
		}
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT p.status FROM projects p JOIN tenants t ON t.id=p.tenant_id WHERE p.tenant_id=? AND p.id=?`, o.Tenant, o.Project).Scan(&status); err != nil || status != "active" {
		return s, errors.New("target tenant/project is missing or project is not active")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,code,COALESCE(parent_id,''),status,source,external_id,sort_order FROM departments WHERE tenant_id=? ORDER BY id`, o.Tenant)
	if err != nil {
		return s, errors.New("cannot read migrated department schema")
	}
	for rows.Next() {
		var d departmentState
		if err = rows.Scan(&d.ID, &d.Name, &d.Code, &d.ParentID, &d.Status, &d.Source, &d.ExternalID, &d.SortOrder); err != nil {
			rows.Close()
			return s, errors.New("invalid department data")
		}
		s.Departments = append(s.Departments, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, errors.New("department read failed")
	}
	rows, err = tx.QueryContext(ctx, `SELECT u.id,u.name,u.email,u.department,u.active,u.operation_disabled,COALESCE(tm.role,''),COALESCE(tm.status,''),COALESCE(pm.role,''),COALESCE(m.role,'') FROM users u LEFT JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=? LEFT JOIN memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id AND m.project_id=? WHERE u.tenant_id=? ORDER BY u.id`, o.Project, o.Project, o.Tenant)
	if err != nil {
		return s, errors.New("cannot read migrated member schema")
	}
	seen := map[string]bool{}
	for rows.Next() {
		p := memberState{Departments: []memberDepartment{}}
		if err = rows.Scan(&p.ID, &p.Name, &p.Email, &p.Department, &p.Active, &p.OperationDisabled, &p.TenantRole, &p.TenantStatus, &p.ProjectRole, &p.LegacyRole); err != nil || seen[p.ID] {
			rows.Close()
			return s, errors.New("invalid or duplicated member identity data")
		}
		seen[p.ID] = true
		s.Members = append(s.Members, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, errors.New("member read failed")
	}
	for i := range s.Members {
		rows, err = tx.QueryContext(ctx, `SELECT department_id,status,is_primary FROM department_memberships WHERE tenant_id=? AND user_id=? ORDER BY department_id`, o.Tenant, s.Members[i].ID)
		if err != nil {
			return s, errors.New("cannot read department assignments")
		}
		for rows.Next() {
			var d memberDepartment
			if err = rows.Scan(&d.ID, &d.Status, &d.Primary); err != nil {
				rows.Close()
				return s, errors.New("invalid department assignment data")
			}
			s.Members[i].Departments = append(s.Members[i].Departments, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return s, errors.New("department assignment read failed")
		}
	}
	return s, nil
}

func planBootstrap(ctx context.Context, tx *sql.Tx, o bootstrapOptions, m bootstrapManifest) (bootstrapPlan, error) {
	p := bootstrapPlan{Mode: "dry-run", TenantID: o.Tenant, ProjectID: o.Project, ManifestHash: digest(m), Departments: []departmentChange{}, Members: []memberChange{}, Warnings: []string{}}
	state, err := readBootstrapState(ctx, tx, o)
	if err != nil {
		return p, err
	}
	p.stateHash = digest(state)
	byID, byCode, keys := map[string]departmentState{}, map[string]departmentState{}, map[string]string{}
	for _, d := range state.Departments {
		code := strings.ToLower(d.Code)
		if _, exists := byCode[code]; exists {
			return p, errors.New("existing department codes are ambiguous")
		}
		byID[d.ID], byCode[code] = d, d
	}
	for _, d := range m.Departments {
		id := d.ExistingID
		if id != "" {
			if _, exists := byID[id]; !exists {
				return p, fmt.Errorf("department %s existing ID is outside the tenant or missing", d.Key)
			}
		} else if existing, exists := byCode[strings.ToLower("BOOT-"+d.Key)]; exists {
			if existing.Source != "bootstrap" || existing.ExternalID != "org-bootstrap:"+d.Key {
				return p, fmt.Errorf("department %s code conflicts with an unrelated directory record", d.Key)
			}
			id = existing.ID
		} else {
			id = bootstrapID("dept_boot_", o.Tenant, d.Key)
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM departments WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
				return p, errors.New("generated department identity conflicts or cannot be verified")
			}
		}
		for key, existingID := range keys {
			if existingID == id {
				return p, fmt.Errorf("department keys %s and %s address the same record", key, d.Key)
			}
		}
		keys[d.Key] = id
	}
	for _, d := range m.Departments {
		id := keys[d.Key]
		before, exists := byID[id]
		after := departmentState{ID: id, Name: d.Name, Code: "BOOT-" + d.Key, ParentID: keys[d.ParentKey], Status: "active", Source: "bootstrap", ExternalID: "org-bootstrap:" + d.Key, SortOrder: d.SortOrder}
		change := departmentChange{Key: d.Key, Action: "create", After: after}
		if exists {
			if before.Status != "active" {
				return p, fmt.Errorf("department %s is disabled; reactivate it explicitly before importing", d.Key)
			}
			after.Code, after.Source, after.ExternalID = before.Code, before.Source, before.ExternalID
			change.Before, change.After, change.Action = &before, after, "update"
			if digest(before) == digest(after) {
				change.Action = "unchanged"
			}
		}
		byID[id] = after
		p.Departments = append(p.Departments, change)
	}
	for _, d := range byID {
		seen := map[string]bool{d.ID: true}
		for parent := d.ParentID; parent != ""; {
			ancestor, exists := byID[parent]
			if !exists || seen[parent] {
				return p, errors.New("resulting department hierarchy is missing a parent or cyclic")
			}
			seen[parent], parent = true, ancestor.ParentID
		}
		for _, other := range byID {
			if other.ID != d.ID && other.ParentID == d.ParentID && other.Name == d.Name {
				return p, errors.New("resulting department hierarchy has duplicate sibling names; specify existingId rather than guessing")
			}
		}
	}
	admins := map[string]bool{}
	for _, email := range m.Administrators {
		admins[email] = true
	}
	for _, member := range m.Members {
		matches := []memberState{}
		for _, current := range state.Members {
			if strings.EqualFold(current.Email, member.Email) {
				matches = append(matches, current)
			}
		}
		if len(matches) > 1 {
			return p, fmt.Errorf("member %s email matches multiple existing accounts", member.Name)
		}
		var before *memberState
		after := memberState{ID: bootstrapID("u_boot_", o.Tenant, member.Email), Name: member.Name, Email: member.Email, TenantRole: "member", TenantStatus: "disabled", ProjectRole: member.ProjectRole, LegacyRole: member.ProjectRole, Departments: []memberDepartment{}}
		if len(matches) == 1 {
			current := matches[0]
			if member.ExistingUserID != "" && member.ExistingUserID != current.ID {
				return p, fmt.Errorf("member %s does not match expected existing user ID", member.Name)
			}
			if current.Name != member.Name && (member.ExpectedName == "" || member.ExpectedName != current.Name) {
				return p, fmt.Errorf("member %s rename requires the exact expectedName", member.Name)
			}
			if current.TenantRole != "member" && current.TenantRole != "tenant_admin" {
				return p, fmt.Errorf("member %s has invalid or missing tenant membership", member.Name)
			}
			before = &current
			after.ID, after.Email, after.Active, after.OperationDisabled, after.TenantRole, after.TenantStatus = current.ID, current.Email, current.Active, current.OperationDisabled, current.TenantRole, current.TenantStatus
			if current.TenantRole == "tenant_admin" && !admins[member.Email] {
				p.Warnings = append(p.Warnings, "Existing administrator retained without demotion: "+current.ID)
			}
		} else {
			if member.ExistingUserID != "" || member.ExpectedName != "" {
				return p, fmt.Errorf("member %s expected account was not found by full email", member.Name)
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=?`, after.ID).Scan(&count); err != nil || count != 0 {
				return p, errors.New("generated member identity conflicts or cannot be verified")
			}
		}
		for _, current := range state.Members {
			if current.ID != after.ID && current.Name == member.Name {
				return p, fmt.Errorf("member %s name collides with a different login email; no name-based merge is allowed", member.Name)
			}
		}
		if admins[member.Email] {
			after.TenantRole = "tenant_admin"
		}
		desired := map[string]bool{}
		for _, key := range member.DepartmentKeys {
			desired[keys[key]] = true
		}
		primary := keys[member.PrimaryDepartmentKey]
		if primary != "" {
			after.Department = byID[primary].Name
		}
		if before != nil {
			for _, old := range before.Departments {
				if _, exists := byID[old.ID]; !exists {
					return p, fmt.Errorf("member %s has an invalid cross-tenant or missing department assignment", member.Name)
				}
				if !desired[old.ID] {
					old.Status, old.Primary = "inactive", false
					after.Departments = append(after.Departments, old)
				}
			}
		}
		for id := range desired {
			after.Departments = append(after.Departments, memberDepartment{ID: id, Status: "active", Primary: id == primary})
		}
		sort.Slice(after.Departments, func(i, j int) bool { return after.Departments[i].ID < after.Departments[j].ID })
		change := memberChange{Action: "create", Before: before, After: after}
		if before != nil {
			change.Action = "update"
			if digest(before) == digest(after) {
				change.Action = "unchanged"
			}
		}
		p.Members = append(p.Members, change)
	}
	for _, d := range p.Departments {
		if d.Action != "unchanged" {
			p.Changes++
		}
	}
	for _, u := range p.Members {
		if u.Action != "unchanged" {
			p.Changes++
		}
	}
	if o.Apply && p.Changes == 0 {
		p.Mode = "unchanged"
	}
	return p, nil
}

func applyBootstrap(ctx context.Context, tx *sql.Tx, o bootstrapOptions, p bootstrapPlan, hashes map[string]string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	audit := func(object, id, action string, before, after any) error {
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(map[string]any{"manifestHash": p.ManifestHash, "value": after})
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,'org-bootstrap-cli',?,?,?,?,?,?)`, o.Tenant, o.Project, object, id, action, string(b), string(a), now)
		return err
	}
	for _, d := range p.Departments {
		if d.Action == "unchanged" {
			continue
		}
		v := d.After
		var parent any
		if v.ParentID != "" {
			parent = v.ParentID
		}
		var err error
		if d.Action == "create" {
			_, err = tx.ExecContext(ctx, `INSERT INTO departments(id,tenant_id,parent_id,name,code,source,external_id,status,sort_order,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.ID, o.Tenant, parent, v.Name, v.Code, v.Source, v.ExternalID, v.Status, v.SortOrder, now, now)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE departments SET parent_id=?,name=?,sort_order=?,updated_at=? WHERE tenant_id=? AND id=?`, parent, v.Name, v.SortOrder, now, o.Tenant, v.ID)
		}
		if err != nil {
			return errors.New("could not save department; entire import rolled back")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET department=? WHERE tenant_id=? AND EXISTS(SELECT 1 FROM department_memberships dm WHERE dm.tenant_id=users.tenant_id AND dm.user_id=users.id AND dm.department_id=? AND dm.is_primary=1 AND dm.status='active')`, v.Name, o.Tenant, v.ID); err != nil {
			return errors.New("could not synchronize department display names")
		}
		if err = audit("department", v.ID, "organization.bootstrap.department_"+d.Action, d.Before, d.After); err != nil {
			return errors.New("could not audit department; entire import rolled back")
		}
	}
	for _, change := range p.Members {
		if change.Action == "unchanged" {
			continue
		}
		v := change.After
		var err error
		if change.Action == "create" {
			hash := hashes[v.ID]
			if hash == "" {
				return errors.New("missing inaccessible new-account credential")
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO users(id,tenant_id,name,email,active,operation_disabled,must_change_password,department,employee_no,last_active,password_hash,password_changed_at,directory_source,directory_external_id)VALUES(?,?,?,?,0,0,1,?,'','',?,?,'local',?)`, v.ID, o.Tenant, v.Name, v.Email, v.Department, hash, now, "org-bootstrap:"+v.ID)
		} else {
			// Deliberately no password, login email, active state, operation flag,
			// personal preference, session or external-directory identity columns.
			_, err = tx.ExecContext(ctx, `UPDATE users SET name=?,department=? WHERE tenant_id=? AND id=?`, v.Name, v.Department, o.Tenant, v.ID)
		}
		if err != nil {
			return errors.New("could not save member; entire import rolled back")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,user_id) DO UPDATE SET role=excluded.role,updated_at=excluded.updated_at`, o.Tenant, v.ID, v.TenantRole, v.TenantStatus, now, now)
		if err != nil {
			return errors.New("could not save tenant membership")
		}
		for _, dep := range v.Departments {
			_, err = tx.ExecContext(ctx, `INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,status,joined_at,updated_at)VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,department_id,user_id) DO UPDATE SET is_primary=excluded.is_primary,status=excluded.status,updated_at=excluded.updated_at`, o.Tenant, dep.ID, v.ID, dep.Primary, dep.Status, now, now)
			if err != nil {
				return errors.New("could not save department membership")
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id) DO UPDATE SET role=excluded.role,updated_at=excluded.updated_at`, o.Tenant, o.Project, v.ID, v.ProjectRole, now, now)
		if err != nil {
			return errors.New("could not save project role")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id) DO UPDATE SET role=excluded.role`, o.Tenant, o.Project, v.ID, v.LegacyRole)
		if err != nil {
			return errors.New("could not save legacy project role")
		}
		if err = audit("user", v.ID, "organization.bootstrap.member_"+change.Action, change.Before, change.After); err != nil {
			return errors.New("could not audit member; entire import rolled back")
		}
	}
	return nil
}
