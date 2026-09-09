package main

import "encoding/json"

func (a *App) customFieldsUsing(query requirementPeopleQuery, object string, id int64) (map[string]any, error) {
	rows, err := query.Query(`SELECT d.key,v.value_json FROM field_values v JOIN field_definitions d ON d.id=v.field_definition_id AND d.tenant_id=v.tenant_id AND d.project_id=v.project_id AND d.object_type=v.object_type AND d.deleted_at='' WHERE v.tenant_id=? AND v.project_id=? AND v.object_type=? AND v.object_id=?`, tenantID, a.pid(), object, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]any{}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var value any
		if err = json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}
