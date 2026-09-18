package main

import (
	"fmt"

	"github.com/e7217/edg/adapters/go/sdk"
)

// ProtocolOPCUA is the point-list protocol this adapter reads.
const ProtocolOPCUA = "opcua"

// nodesFromPoints turns a declared point list into the nodes to read. A point's
// address is its NodeId; encoding is not used.
func nodesFromPoints(pl *sdk.PointList) ([]Node, error) {
	if pl.Protocol != "" && pl.Protocol != ProtocolOPCUA {
		return nil, fmt.Errorf("point list protocol is %q; this adapter reads %q", pl.Protocol, ProtocolOPCUA)
	}
	points := pl.EnabledPoints()
	nodes := make([]Node, 0, len(points))
	for _, p := range points {
		nodes = append(nodes, Node{Name: p.Name, NodeID: p.Address, Unit: p.Unit})
	}
	if err := parseNodes(nodes); err != nil {
		return nil, err
	}
	return nodes, nil
}
