"""Tests for Asset Relations - Compatible with Go implementation

Tests for RelationType enum and AssetRelation dataclass following
the TDD approach. These tests validate:
1. RelationType enum values match Go constants
2. AssetRelation dataclass structure
3. JSON serialization with to_dict()
4. Metadata omission when empty
"""

from __future__ import annotations

import time
import unittest

from ..models import AssetRelation, RelationType


class TestRelationType(unittest.TestCase):
    """Test RelationType enum"""

    def test_relation_type_values(self):
        """RelationType should have correct constant values matching Go"""
        self.assertEqual(RelationType.PART_OF, "partOf")
        self.assertEqual(RelationType.CONNECTED_TO, "connectedTo")
        self.assertEqual(RelationType.LOCATED_IN, "locatedIn")

    def test_relation_type_all_types(self):
        """Should have exactly 3 relation types"""
        all_types = [
            RelationType.PART_OF,
            RelationType.CONNECTED_TO,
            RelationType.LOCATED_IN,
        ]
        self.assertEqual(len(all_types), 3)


class TestAssetRelation(unittest.TestCase):
    """Test AssetRelation dataclass"""

    def test_asset_relation_structure(self):
        """AssetRelation should have all required fields"""
        relation = AssetRelation(
            id="rel-001",
            source_asset_id="asset-001",
            target_asset_id="asset-002",
            relation_type=RelationType.PART_OF,
            created_at=int(time.time() * 1000),
        )

        self.assertEqual(relation.id, "rel-001")
        self.assertEqual(relation.source_asset_id, "asset-001")
        self.assertEqual(relation.target_asset_id, "asset-002")
        self.assertEqual(relation.relation_type, RelationType.PART_OF)
        self.assertIsNotNone(relation.created_at)
        self.assertIsNone(relation.metadata)

    def test_asset_relation_with_metadata(self):
        """AssetRelation should support metadata dictionary"""
        relation = AssetRelation(
            id="rel-002",
            source_asset_id="asset-001",
            target_asset_id="asset-002",
            relation_type=RelationType.CONNECTED_TO,
            created_at=int(time.time() * 1000),
            metadata={"installed_date": "2025-01-15", "location": "building-a"},
        )

        self.assertIsNotNone(relation.metadata)
        self.assertEqual(relation.metadata["installed_date"], "2025-01-15")
        self.assertEqual(relation.metadata["location"], "building-a")

    def test_asset_relation_to_dict(self):
        """AssetRelation.to_dict() should serialize correctly"""
        timestamp = int(time.time() * 1000)
        relation = AssetRelation(
            id="rel-003",
            source_asset_id="asset-001",
            target_asset_id="asset-002",
            relation_type=RelationType.LOCATED_IN,
            created_at=timestamp,
            metadata={"floor": "3"},
        )

        result = relation.to_dict()

        self.assertEqual(result["id"], "rel-003")
        self.assertEqual(result["source_asset_id"], "asset-001")
        self.assertEqual(result["target_asset_id"], "asset-002")
        self.assertEqual(result["relation_type"], "locatedIn")
        self.assertEqual(result["created_at"], timestamp)
        self.assertEqual(result["metadata"], {"floor": "3"})

    def test_asset_relation_to_dict_omit_empty_metadata(self):
        """AssetRelation.to_dict() should omit None metadata"""
        relation = AssetRelation(
            id="rel-004",
            source_asset_id="asset-001",
            target_asset_id="asset-002",
            relation_type=RelationType.PART_OF,
            created_at=int(time.time() * 1000),
            metadata=None,
        )

        result = relation.to_dict()

        self.assertNotIn("metadata", result)
        self.assertEqual(len(result), 5)  # id, source, target, type, created_at


if __name__ == "__main__":
    unittest.main()
