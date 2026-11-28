package core

import (
	"testing"
)

func viaNodes(self string, others ...CanvasNode) []CanvasNode {
	return append([]CanvasNode{{Name: "me", Addr: "10.0.0.1:1", Self: true, Gw: self, Reachable: true}}, others...)
}

func peer(name, gw string) CanvasNode {
	return CanvasNode{Name: name, Addr: "10.0.0." + name + ":1", Gw: gw, Reachable: true}
}

// The serving node's own member is "local" to it; borrowed by a paused node it must not read as this node,
// or the controller mark lands on the node the page is open on.
func TestTakeFromClearsLocalOnMembers(t *testing.T) {
	mine := CanvasGateway{GroupID: 1, Nodes: viaNodes("paused")}
	theirs := CanvasGateway{GroupID: 1, Members: []CanvasMember{{IP: "10.0.0.3", Local: true, AGC: true}, {IP: "10.0.0.4"}}}
	mine.TakeFrom(theirs, peer("3", "ok"))
	for _, m := range mine.Members {
		if m.Local {
			t.Errorf("%s still local", m.IP)
		}
	}
	if !mine.Members[0].AGC || !theirs.Members[0].Local || !mine.Members[0].ViaLocal || mine.Members[1].ViaLocal {
		t.Errorf("the controller flag must stay, and the serving node's own copy must not change: %+v %+v", mine.Members, theirs.Members)
	}
}

func TestTakeFromKeepsThisNodesOwnEntry(t *testing.T) {
	mine := CanvasGateway{GroupID: 1, Name: "x", Status: "idle", Detail: "not running here", Nodes: viaNodes("notserving"), Paused: true, NodePaused: true}
	theirs := CanvasGateway{GroupID: 1, Status: "ok", Detail: "running and answering", Anycast: []AnycastState{{Addr: "192.0.2.1", Up: true}}, Families: []CanvasFamily{{AF: "v4", Status: "ok"}}}
	mine.TakeFrom(theirs, peer("2", "ok"))
	if mine.Status != "ok" || mine.Detail != "running and answering" || len(mine.Anycast) != 1 || len(mine.Families) != 1 {
		t.Fatalf("not taken: %+v", mine)
	}
	if mine.Via != "2" || mine.ViaAddr != "10.0.0.2:1" || len(mine.Nodes) != 1 || mine.Name != "x" || !mine.Paused {
		t.Fatalf("this node's own part changed: %+v", mine)
	}
}
