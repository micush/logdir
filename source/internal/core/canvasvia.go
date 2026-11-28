package core

// takeFrom puts the serving node's picture of the gateway in place of this node's: its colour, anycast
// addresses, members and uptime; the cluster's nodes and everything about this node's own entry stay.
func (g *CanvasGateway) TakeFrom(p CanvasGateway, from CanvasNode) {
	g.Status, g.Detail, g.Families = p.Status, p.Detail, p.Families
	g.Members, g.Anycast, g.Uptime = p.Members, p.Anycast, p.Uptime
	// the serving node's own entry is "local" to it, not to this node: nobody is this node here, or the
	// page would put the controller mark (and the star) on the node it is open on
	g.Members = make([]CanvasMember, len(p.Members))
	copy(g.Members, p.Members)
	for i := range g.Members {
		g.Members[i].ViaLocal = g.Members[i].Local
		g.Members[i].Local = false
	}
	g.Via, g.ViaAddr = from.Name, from.Addr
}
