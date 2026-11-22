package app

import (
	"github.com/kranz-org/kranz/internal/config"
	"github.com/kranz-org/kranz/internal/service"
)

// Graph edge kinds. An edge always points from what must happen first to what
// depends on it, so a reader can follow the same direction for every kind.
const (
	GraphEdgeDependency   = "dependency"
	GraphEdgePrerequisite = "prerequisite"
	GraphEdgeOwns         = "owns"
)

// Graph node kinds.
const (
	GraphNodeService = "service"
	GraphNodeAction  = "action"
	GraphNodeGroup   = "group"
)

// GraphNode is one addressable thing in the project. ID is the same address
// every other surface uses: a service name, or OWNER/ACTION for an action.
type GraphNode struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// State is the live status of a service node, empty for nodes that have
	// none. It is included so a reader does not have to join the graph against
	// a separate status call to see where a chain is stuck.
	State config.ServiceStatus `json:"state,omitempty"`
}

// GraphEdge connects two nodes by ID.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
	// Condition is the dependency condition a dependency edge waits for, and
	// Run is the frequency policy of a prerequisite edge.
	Condition string `json:"condition,omitempty"`
	Run       string `json:"run,omitempty"`
}

// Graph is the project's declared structure with live service state folded in.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Graph implements API.Graph.
func (l *Local) Graph() Graph {
	cfg := l.Config()
	graph := Graph{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	for _, name := range cfg.ServiceNames() {
		service := cfg.Services[name]
		node := GraphNode{ID: name, Kind: GraphNodeService, Description: service.Description, Tags: service.Tags}
		if snapshot, ok := l.Service(name); ok {
			node.State = snapshot.State.Status
		}
		graph.Nodes = append(graph.Nodes, node)
		for _, dependency := range service.DependsOn {
			edge := GraphEdge{From: dependency, To: name, Type: GraphEdgeDependency}
			if condition, ok := service.DependencyConditions[dependency]; ok {
				edge.Condition = string(condition.Condition)
			}
			graph.Edges = append(graph.Edges, edge)
		}
		for _, prerequisite := range service.BeforeStart {
			id := prerequisite.ActionID(name)
			graph.Edges = append(graph.Edges, GraphEdge{From: id.Owner + "/" + id.Name, To: name, Type: GraphEdgePrerequisite, Run: string(prerequisite.RunPolicy())})
		}
	}
	for _, name := range cfg.ActionGroupNames() {
		graph.Nodes = append(graph.Nodes, GraphNode{ID: name, Kind: GraphNodeGroup, Description: cfg.ActionGroups[name].Description})
	}
	for _, id := range cfg.ActionIDs() {
		action, _ := cfg.ResolveAction(id)
		address := id.Owner + "/" + id.Name
		graph.Nodes = append(graph.Nodes, GraphNode{ID: address, Kind: GraphNodeAction, Description: action.Description})
		graph.Edges = append(graph.Edges, GraphEdge{From: id.Owner, To: address, Type: GraphEdgeOwns})
	}
	return graph
}

// ServiceDependencyOrder returns the manager's service order and readiness
// waves for a configuration. Clients can use both without accessing a Manager.
func ServiceDependencyOrder(cfg *config.Config) ([]string, [][]string, error) {
	order, err := service.TopologicalOrder(cfg)
	if err != nil {
		return nil, nil, err
	}
	return order, service.DependencyLevels(cfg, order), nil
}
