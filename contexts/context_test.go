package contexts_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// JSONLDContext represents the @context section of a JSON-LD document
type JSONLDContext struct {
	Version float64 `json:"@version"`
	Vocab   string  `json:"@vocab"`
	// Standard prefixes that should be defined
	RDFS   string `json:"rdfs"`
	RDF    string `json:"rdf"`
	SOSA   string `json:"sosa"`
	SSN    string `json:"ssn"`
	QUDT   string `json:"qudt"`
	Schema string `json:"schema"`
	XSD    string `json:"xsd"`
}

// JSONLDDocument represents a JSON-LD document structure
type JSONLDDocument struct {
	Context json.RawMessage `json:"@context"`
	Graph   json.RawMessage `json:"@graph"`
}

// TestJSONLDContextPrefixes verifies that all prefixes used in @graph are defined in @context
func TestJSONLDContextPrefixes(t *testing.T) {
	// Read the JSON-LD file
	data, err := os.ReadFile("edg-context.jsonld")
	if err != nil {
		t.Fatalf("Failed to read edg-context.jsonld: %v", err)
	}

	// Parse as JSON-LD document
	var doc JSONLDDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Failed to parse JSON-LD document: %v", err)
	}

	// Parse context to map for checking prefixes
	var contextMap map[string]interface{}
	if err := json.Unmarshal(doc.Context, &contextMap); err != nil {
		t.Fatalf("Failed to parse @context: %v", err)
	}

	// Check if graph references use defined prefixes
	graphStr := string(doc.Graph)

	// List of prefixes used in @graph that must be defined in @context
	requiredPrefixes := []struct {
		prefix string
		usage  string
	}{
		{"rdfs", "rdfs:Class, rdfs:label, rdfs:comment, rdfs:subClassOf, rdfs:domain, rdfs:range"},
		{"rdf", "rdf:Property"},
	}

	for _, req := range requiredPrefixes {
		// Check if the prefix is used in @graph
		if strings.Contains(graphStr, req.prefix+":") {
			// Verify the prefix is defined in @context
			if _, exists := contextMap[req.prefix]; !exists {
				t.Errorf("Prefix '%s' is used in @graph (%s) but not defined in @context", req.prefix, req.usage)
			}
		}
	}
}

// TestJSONLDContextValidPrefixURIs verifies that all prefix URIs are valid
func TestJSONLDContextValidPrefixURIs(t *testing.T) {
	data, err := os.ReadFile("edg-context.jsonld")
	if err != nil {
		t.Fatalf("Failed to read edg-context.jsonld: %v", err)
	}

	var doc JSONLDDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Failed to parse JSON-LD document: %v", err)
	}

	var contextMap map[string]interface{}
	if err := json.Unmarshal(doc.Context, &contextMap); err != nil {
		t.Fatalf("Failed to parse @context: %v", err)
	}

	// Expected standard prefix URIs
	expectedPrefixes := map[string]string{
		"rdfs":   "http://www.w3.org/2000/01/rdf-schema#",
		"rdf":    "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
		"sosa":   "http://www.w3.org/ns/sosa/",
		"ssn":    "http://www.w3.org/ns/ssn/",
		"qudt":   "http://qudt.org/schema/qudt/",
		"schema": "http://schema.org/",
		"xsd":    "http://www.w3.org/2001/XMLSchema#",
	}

	for prefix, expectedURI := range expectedPrefixes {
		if val, exists := contextMap[prefix]; exists {
			if uri, ok := val.(string); ok {
				if uri != expectedURI {
					t.Errorf("Prefix '%s' has incorrect URI: got '%s', expected '%s'", prefix, uri, expectedURI)
				}
			}
		} else {
			t.Errorf("Required prefix '%s' is not defined in @context", prefix)
		}
	}
}

// TestJSONLDValidJSON verifies the file is valid JSON
func TestJSONLDValidJSON(t *testing.T) {
	data, err := os.ReadFile("edg-context.jsonld")
	if err != nil {
		t.Fatalf("Failed to read edg-context.jsonld: %v", err)
	}

	var doc interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Errorf("Invalid JSON: %v", err)
	}
}
