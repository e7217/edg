package core

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

func (s *Store) GetAncestors(assetID string, relTypes []RelationType, maxDepth int) ([]*AssetNode, error) {
	relTypes, maxDepth, err := normalizeTraversalArgs(relTypes, maxDepth)
	if err != nil {
		return nil, err
	}

	placeholders := relationTypePlaceholders(relTypes)
	query := fmt.Sprintf(`
WITH RECURSIVE traversal(id, name, template_name, depth, relation_type, parent_id, path) AS (
	SELECT a.id, a.name, a.template_name, 1, r.relation_type, r.source_asset_id,
	       ',' || r.source_asset_id || ',' || r.target_asset_id || ','
	FROM asset_relations r
	JOIN assets a ON a.id = r.target_asset_id
	WHERE r.source_asset_id = ? AND r.relation_type IN (%s)
	UNION ALL
	SELECT a.id, a.name, a.template_name, t.depth + 1, r.relation_type, r.source_asset_id,
	       t.path || r.target_asset_id || ','
	FROM traversal t
	JOIN asset_relations r ON r.source_asset_id = t.id
	JOIN assets a ON a.id = r.target_asset_id
	WHERE t.depth < ? AND r.relation_type IN (%s)
	  AND instr(t.path, ',' || r.target_asset_id || ',') = 0
)
SELECT id, name, template_name, depth, relation_type, parent_id, '' AS direction
FROM traversal
ORDER BY depth ASC, name ASC, id ASC`, placeholders, placeholders)

	args := []any{assetID}
	args = appendRelationTypes(args, relTypes)
	args = append(args, maxDepth)
	args = appendRelationTypes(args, relTypes)

	return s.queryAssetNodes(query, args...)
}

func (s *Store) GetDescendants(assetID string, relTypes []RelationType, maxDepth int) ([]*AssetNode, error) {
	relTypes, maxDepth, err := normalizeTraversalArgs(relTypes, maxDepth)
	if err != nil {
		return nil, err
	}

	placeholders := relationTypePlaceholders(relTypes)
	query := fmt.Sprintf(`
WITH RECURSIVE traversal(id, name, template_name, depth, relation_type, parent_id, path) AS (
	SELECT a.id, a.name, a.template_name, 1, r.relation_type, r.target_asset_id,
	       ',' || r.target_asset_id || ',' || r.source_asset_id || ','
	FROM asset_relations r
	JOIN assets a ON a.id = r.source_asset_id
	WHERE r.target_asset_id = ? AND r.relation_type IN (%s)
	UNION ALL
	SELECT a.id, a.name, a.template_name, t.depth + 1, r.relation_type, r.target_asset_id,
	       t.path || r.source_asset_id || ','
	FROM traversal t
	JOIN asset_relations r ON r.target_asset_id = t.id
	JOIN assets a ON a.id = r.source_asset_id
	WHERE t.depth < ? AND r.relation_type IN (%s)
	  AND instr(t.path, ',' || r.source_asset_id || ',') = 0
)
SELECT id, name, template_name, depth, relation_type, parent_id, '' AS direction
FROM traversal
ORDER BY depth ASC, name ASC, id ASC`, placeholders, placeholders)

	args := []any{assetID}
	args = appendRelationTypes(args, relTypes)
	args = append(args, maxDepth)
	args = appendRelationTypes(args, relTypes)

	return s.queryAssetNodes(query, args...)
}

func (s *Store) GetConnected(assetID string, relType RelationType) ([]*AssetNode, error) {
	relTypes := []RelationType{}
	if relType != "" {
		relTypes = []RelationType{relType}
	}
	relTypes, _, err := normalizeTraversalArgs(relTypes, DefaultTraversalMaxDepth)
	if err != nil {
		return nil, err
	}

	placeholders := relationTypePlaceholders(relTypes)
	query := fmt.Sprintf(`
SELECT id, name, template_name, depth, relation_type, parent_id, direction
FROM (
	SELECT a.id AS id, a.name AS name, a.template_name AS template_name, 1 AS depth,
	       r.relation_type AS relation_type, r.source_asset_id AS parent_id, ? AS direction
	FROM asset_relations r
	JOIN assets a ON a.id = r.target_asset_id
	WHERE r.source_asset_id = ? AND r.relation_type IN (%s)
	UNION
	SELECT a.id AS id, a.name AS name, a.template_name AS template_name, 1 AS depth,
	       r.relation_type AS relation_type, r.target_asset_id AS parent_id, ? AS direction
	FROM asset_relations r
	JOIN assets a ON a.id = r.source_asset_id
	WHERE r.target_asset_id = ? AND r.relation_type IN (%s)
)
ORDER BY name ASC, id ASC`, placeholders, placeholders)

	args := []any{DirectionOutgoing, assetID}
	args = appendRelationTypes(args, relTypes)
	args = append(args, DirectionIncoming, assetID)
	args = appendRelationTypes(args, relTypes)

	return s.queryAssetNodes(query, args...)
}

func (s *Store) GetSubtree(assetID string, relTypes []RelationType, maxDepth int) (*AssetTreeNode, error) {
	root, err := s.GetAsset(assetID)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, nil
	}

	descendants, err := s.GetDescendants(assetID, relTypes, maxDepth)
	if err != nil {
		return nil, err
	}

	rootNode := &AssetTreeNode{
		ID:           root.ID,
		Name:         root.Name,
		TemplateName: root.TemplateName,
		Depth:        0,
	}
	nodes := map[string]*AssetTreeNode{root.ID: rootNode}

	for _, descendant := range descendants {
		nodes[descendant.ID] = &AssetTreeNode{
			ID:           descendant.ID,
			Name:         descendant.Name,
			TemplateName: descendant.TemplateName,
			Depth:        descendant.Depth,
			RelationType: descendant.RelationType,
			ParentID:     descendant.ParentID,
		}
	}

	for _, descendant := range descendants {
		node := nodes[descendant.ID]
		parent := nodes[descendant.ParentID]
		if parent == nil {
			continue
		}
		parent.Children = append(parent.Children, node)
	}

	sortTree(rootNode)
	return rootNode, nil
}

func (s *Store) queryAssetNodes(query string, args ...any) ([]*AssetNode, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query asset traversal: %w", err)
	}
	defer rows.Close()

	nodes := []*AssetNode{}
	for rows.Next() {
		var node AssetNode
		var templateName sql.NullString
		if err := rows.Scan(
			&node.ID,
			&node.Name,
			&templateName,
			&node.Depth,
			&node.RelationType,
			&node.ParentID,
			&node.Direction,
		); err != nil {
			return nil, fmt.Errorf("failed to scan asset traversal: %w", err)
		}
		if templateName.Valid {
			node.TemplateName = templateName.String
		}
		nodes = append(nodes, &node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate asset traversal: %w", err)
	}
	return nodes, nil
}

func normalizeTraversalArgs(relTypes []RelationType, maxDepth int) ([]RelationType, int, error) {
	if len(relTypes) == 0 {
		relTypes = ValidRelationTypes()
	}
	normalized := make([]RelationType, 0, len(relTypes))
	seen := make(map[RelationType]bool, len(relTypes))
	for _, relType := range relTypes {
		if !IsValidRelationType(relType) {
			return nil, 0, fmt.Errorf("invalid relation type: %s", relType)
		}
		if seen[relType] {
			continue
		}
		seen[relType] = true
		normalized = append(normalized, relType)
	}
	if maxDepth <= 0 {
		maxDepth = DefaultTraversalMaxDepth
	}
	return normalized, maxDepth, nil
}

func relationTypePlaceholders(relTypes []RelationType) string {
	return strings.TrimRight(strings.Repeat("?,", len(relTypes)), ",")
}

func appendRelationTypes(args []any, relTypes []RelationType) []any {
	for _, relType := range relTypes {
		args = append(args, relType)
	}
	return args
}

func sortTree(node *AssetTreeNode) {
	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].Name == node.Children[j].Name {
			return node.Children[i].ID < node.Children[j].ID
		}
		return node.Children[i].Name < node.Children[j].Name
	})
	for _, child := range node.Children {
		sortTree(child)
	}
}
