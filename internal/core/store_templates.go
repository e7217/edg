package core

import "database/sql"

// UpsertTemplate persists a template and its resources/constraints atomically,
// replacing any existing template with the same name. created_at is preserved.
func (s *Store) UpsertTemplate(t *AssetTemplate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO templates (name, updated_at) VALUES (?, CURRENT_TIMESTAMP)
		 ON CONFLICT(name) DO UPDATE SET updated_at = CURRENT_TIMESTAMP`,
		t.Name,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM template_resources WHERE template_name = ?`, t.Name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM template_constraints WHERE template_name = ?`, t.Name); err != nil {
		return err
	}
	for _, r := range t.Resources {
		if _, err := tx.Exec(
			`INSERT INTO template_resources (template_name, name, value_type, unit) VALUES (?, ?, ?, ?)`,
			t.Name, r.Name, r.ValueType, r.Unit,
		); err != nil {
			return err
		}
	}
	if err := insertTemplateConstraints(tx, t.Name, "required", t.Constraints.RequiredRelations); err != nil {
		return err
	}
	if err := insertTemplateConstraints(tx, t.Name, "forbidden", t.Constraints.ForbiddenRelations); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTemplateConstraints(tx *sql.Tx, templateName, kind string, constraints []RelationConstraint) error {
	for _, c := range constraints {
		if _, err := tx.Exec(
			`INSERT INTO template_constraints (template_name, kind, relation_type, target_template, min_count, max_count)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			templateName, kind, string(c.Type), c.TargetTemplate, nullableInt(c.Min), nullableInt(c.Max),
		); err != nil {
			return err
		}
	}
	return nil
}

func nullableInt(v *int) interface{} {
	if v == nil {
		return nil
	}
	return *v
}

// GetTemplate returns the template by name, or nil if it does not exist.
func (s *Store) GetTemplate(name string) (*AssetTemplate, error) {
	var found string
	err := s.db.QueryRow(`SELECT name FROM templates WHERE name = ?`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t := &AssetTemplate{Name: name}

	rows, err := s.db.Query(
		`SELECT name, value_type, unit FROM template_resources WHERE template_name = ? ORDER BY name`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r AssetResource
		if err := rows.Scan(&r.Name, &r.ValueType, &r.Unit); err != nil {
			rows.Close()
			return nil, err
		}
		t.Resources = append(t.Resources, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	crows, err := s.db.Query(
		`SELECT kind, relation_type, target_template, min_count, max_count
		 FROM template_constraints WHERE template_name = ? ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var kind, relType string
		var c RelationConstraint
		var minV, maxV sql.NullInt64
		if err := crows.Scan(&kind, &relType, &c.TargetTemplate, &minV, &maxV); err != nil {
			return nil, err
		}
		c.Type = RelationType(relType)
		if minV.Valid {
			v := int(minV.Int64)
			c.Min = &v
		}
		if maxV.Valid {
			v := int(maxV.Int64)
			c.Max = &v
		}
		if kind == "required" {
			t.Constraints.RequiredRelations = append(t.Constraints.RequiredRelations, c)
		} else {
			t.Constraints.ForbiddenRelations = append(t.Constraints.ForbiddenRelations, c)
		}
	}
	return t, crows.Err()
}

// ListTemplates returns all templates ordered by name.
func (s *Store) ListTemplates() ([]*AssetTemplate, error) {
	rows, err := s.db.Query(`SELECT name FROM templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	templates := make([]*AssetTemplate, 0, len(names))
	for _, name := range names {
		t, err := s.GetTemplate(name)
		if err != nil {
			return nil, err
		}
		if t != nil {
			templates = append(templates, t)
		}
	}
	return templates, nil
}

// DeleteTemplate removes a template and its children (CASCADE). It is a no-op if
// the template does not exist.
func (s *Store) DeleteTemplate(name string) error {
	_, err := s.db.Exec(`DELETE FROM templates WHERE name = ?`, name)
	return err
}
