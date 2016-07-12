package stitch

// A node in the communiction graph.
type node struct {
	name        string
	label       string
	connections map[string]node
}

// A connection is an edge in the communication graph.
type connection struct {
	from string
	to   string
}

// A graph represents permission to communicate across a series of nodes.
// Each node is a container and each edge is permissions to
// initiate a connection.
type graph struct {
	nodes map[string]node
	// A set of containers which can be placed together on a VM.
	availability []availabilitySet
	// Constraints on which containers can be placed together.
	placement map[string][]string
	machines  []Machine
}

func makeGraph() graph {
	return graph{
		nodes: map[string]node{},
		// One global availability set by default.
		availability: []availabilitySet{{}},
		placement:    map[string][]string{},
		machines:     []Machine{},
	}
}

func (g graph) copyGraph() graph {
	newNodes := map[string]node{}
	for label, node := range g.nodes {
		newNodes[label] = node
	}

	newAvail := make([]availabilitySet, len(g.availability))
	copy(newAvail, g.availability)

	return graph{nodes: newNodes, availability: newAvail}
}

func (g *graph) addConnection(from string, to string) error {
	// from and to are labels
	var fromContainers []node
	var toContainers []node

	for _, node := range g.nodes {
		if node.label == from {
			fromContainers = append(fromContainers, node)
		}
		if node.label == to {
			toContainers = append(toContainers, node)
		}
	}

	for _, fromNode := range fromContainers {
		for _, toNode := range toContainers {
			if fromNode.name != toNode.name {
				fromNode.connections[toNode.name] = toNode
			}
		}
	}

	return nil
}

func (g graph) getNodes() []node {
	var res []node
	for _, n := range g.nodes {
		res = append(res, n)
	}
	return res
}

func (g graph) getConnections() []connection {
	var res []connection
	for _, n := range g.getNodes() {
		for _, edge := range n.connections {
			res = append(res, connection{from: n.name, to: edge.name})
		}
	}
	return res
}

func (g *graph) addNode(cid string, label string) node {
	n := node{
		name:        cid,
		label:       label,
		connections: map[string]node{},
	}
	g.nodes[cid] = n
	g.availability[0].insert(cid)
	g.placeNodes()

	return n
}

func (g *graph) removeNode(label string) {
	delete(g.nodes, label)

	// Delete edges to this node.
	for _, n := range g.getNodes() {
		delete(n.connections, label)
	}
}

// Find all nodes reachable from the given node.
func (n node) dfs() []string {
	reached := map[string]struct{}{}

	var explore func(t node)
	explore = func(t node) {
		for label, node := range t.connections {
			_, explored := reached[label]
			if !explored {
				reached[label] = struct{}{}
				explore(node)
			}
		}
	}
	explore(n)

	var reachable []string
	for l := range reached {
		reachable = append(reachable, string(l))
	}

	return reachable
}

// Compute all the paths between two nodes.
func paths(start node, end node) ([][]string, bool) {
	reach := start.dfs()
	if !contains(reach, end.name) {
		return nil, false
	}

	var paths [][]string

	var explore func(t node, p []string)
	explore = func(t node, p []string) {
		if t.name == end.name {
			paths = append(paths, p)
			return
		}

		for label, node := range t.connections {
			if !contains(p, label) { // Discount self-reachability.
				explore(node, append(p, label))
			}
		}
	}
	explore(start, []string{start.name})
	return paths, true
}
