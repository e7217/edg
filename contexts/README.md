# EDG JSON-LD Context

This directory contains JSON-LD context definitions for the EDG platform, enabling semantic interoperability with standard ontologies.

## Overview

The `edg-context.jsonld` file maps EDG's data model to established semantic web vocabularies:

- **SOSA** (Sensor, Observation, Sample, and Actuator): W3C standard for sensor data
- **SSN** (Semantic Sensor Network): W3C ontology for sensors and observations
- **QUDT** (Quantities, Units, Dimensions and Types): Standard for units and quantities
- **Schema.org**: General-purpose structured data vocabulary

## Relation Type Mappings

### partOf → ssn:isPartOf
```json
{
  "relation_type": "partOf",
  "semantic_mapping": "http://www.w3.org/ns/ssn/isPartOf"
}
```

**Use Case**: Hierarchical relationships where one asset is a component of another.

**Example**: A sensor is part of a monitoring system.
```json
{
  "@context": "https://edg.e7217.io/contexts/edg-context.jsonld",
  "@type": "AssetRelation",
  "source_asset_id": "sensor-001",
  "target_asset_id": "system-001",
  "relation_type": "partOf",
  "metadata": {
    "installation_date": "2025-01-15"
  }
}
```

### connectedTo → sosa:isHostedBy
```json
{
  "relation_type": "connectedTo",
  "semantic_mapping": "http://www.w3.org/ns/sosa/isHostedBy"
}
```

**Use Case**: Peer/network connections between assets, such as sensors hosted by equipment.

**Example**: A temperature sensor is connected to a data logger.
```json
{
  "@context": "https://edg.e7217.io/contexts/edg-context.jsonld",
  "@type": "AssetRelation",
  "source_asset_id": "temp-sensor-001",
  "target_asset_id": "datalogger-001",
  "relation_type": "connectedTo",
  "metadata": {
    "connection_type": "wireless",
    "protocol": "mqtt"
  }
}
```

### locatedIn → schema:containedInPlace
```json
{
  "relation_type": "locatedIn",
  "semantic_mapping": "http://schema.org/containedInPlace"
}
```

**Use Case**: Spatial containment, where one asset is physically located within another.

**Example**: Equipment is located in a building.
```json
{
  "@context": "https://edg.e7217.io/contexts/edg-context.jsonld",
  "@type": "AssetRelation",
  "source_asset_id": "equipment-001",
  "target_asset_id": "building-a",
  "relation_type": "locatedIn",
  "metadata": {
    "floor": "3",
    "room": "301"
  }
}
```

## Usage

### In Python
```python
from sdk import AssetRelation, RelationType

# Create a relation
relation = AssetRelation(
    id="rel-001",
    source_asset_id="sensor-001",
    target_asset_id="system-001",
    relation_type=RelationType.PART_OF,
    created_at=int(time.time() * 1000),
    metadata={"installation_date": "2025-01-15"}
)

# Convert to JSON-LD
data = relation.to_dict()
data["@context"] = "https://edg.e7217.io/contexts/edg-context.jsonld"
data["@type"] = "AssetRelation"
```

### In Go
```go
import "github.com/e7217/edg/internal/core"

// Create a relation
relation := &core.AssetRelation{
    ID:            "rel-001",
    SourceAssetID: "sensor-001",
    TargetAssetID: "system-001",
    RelationType:  core.RelationPartOf,
    CreatedAt:     time.Now(),
    Metadata: map[string]string{
        "installation_date": "2025-01-15",
    },
}
```

## Semantic Interoperability

By using standardized vocabularies, EDG data can be:

1. **Integrated** with other IoT platforms using SOSA/SSN
2. **Queried** using SPARQL across heterogeneous data sources
3. **Validated** against ontology constraints
4. **Enriched** with additional semantic information
5. **Exported** to RDF triple stores for graph analysis

## Validation

The JSON-LD context can be validated using standard tools:

```bash
# Using jsonld-cli (Node.js)
npm install -g jsonld-cli
jsonld format edg-context.jsonld

# Using pyLD (Python)
pip install PyLD
python -c "from pyld import jsonld; import json; jsonld.expand(json.load(open('edg-context.jsonld')))"
```

## References

- [W3C SOSA/SSN Ontology](https://www.w3.org/TR/vocab-ssn/)
- [Schema.org Vocabulary](https://schema.org/)
- [QUDT Ontology](http://www.qudt.org/)
- [JSON-LD 1.1 Specification](https://www.w3.org/TR/json-ld11/)
