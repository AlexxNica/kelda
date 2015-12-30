package cluster

import (
	"fmt"
	"testing"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/minion/pb"
	"github.com/davecgh/go-spew/spew"
)

type clients struct {
	clients  map[string]*fakeClient
	newCalls int
}

func TestBoot(t *testing.T) {
	fm, clients := startTest()
	fm.runOnce()

	if clients.newCalls != 0 {
		t.Errorf("clients.newCalls = %d, want 0", clients.newCalls)
	}

	fm.conn.Transact(func(view *db.Database) error {
		m := view.InsertMachine()
		m.ClusterID = 1
		m.PublicIP = "1.1.1.1"
		m.PrivateIP = "1.1.1.1."
		m.CloudID = "ID"
		m.Write()
		return nil
	})

	fm.runOnce()
	if clients.newCalls != 1 {
		t.Errorf("clients.newCalls = %d, want 1", clients.newCalls)
	}

	if _, ok := clients.clients["1.1.1.1"]; !ok {
		t.Errorf("Missing 1.1.1.1: %s", spew.Sdump(clients))
	}

	fm.runOnce()
	if clients.newCalls != 1 {
		t.Errorf("clients.newCalls = %d, want 1", clients.newCalls)
	}

	if _, ok := clients.clients["1.1.1.1"]; !ok {
		t.Errorf("Missing 1.1.1.1: %s", spew.Sdump(clients))
	}

	fm.conn.Transact(func(view *db.Database) error {
		m := view.InsertMachine()
		m.ClusterID = 1
		m.PublicIP = "2.2.2.2"
		m.PrivateIP = "2.2.2.2"
		m.CloudID = "ID2"
		m.Write()
		return nil
	})

	fm.runOnce()
	if clients.newCalls != 2 {
		t.Errorf("clients.newCalls = %d, want 2", clients.newCalls)
	}

	if _, ok := clients.clients["2.2.2.2"]; !ok {
		t.Errorf("Missing 2.2.2.2: %s", spew.Sdump(clients))
	}
	if _, ok := clients.clients["1.1.1.1"]; !ok {
		t.Errorf("Missing 1.1.1.1: %s", spew.Sdump(clients))
	}

	fm.runOnce()
	fm.runOnce()
	fm.runOnce()
	fm.runOnce()
	if clients.newCalls != 2 {
		t.Errorf("clients.newCalls = %d, want 2", clients.newCalls)
	}

	if _, ok := clients.clients["2.2.2.2"]; !ok {
		t.Errorf("Missing 2.2.2.2: %s", spew.Sdump(clients))
	}
	if _, ok := clients.clients["1.1.1.1"]; !ok {
		t.Errorf("Missing 1.1.1.1: %s", spew.Sdump(clients))
	}

	fm.conn.Transact(func(view *db.Database) error {
		machines := view.SelectFromMachine(func(m db.Machine) bool {
			return m.PublicIP == "1.1.1.1"
		})
		machines[0].Remove()
		return nil
	})

	fm.runOnce()
	if clients.newCalls != 2 {
		t.Errorf("clients.newCalls = %d, want 2", clients.newCalls)
	}

	if _, ok := clients.clients["2.2.2.2"]; !ok {
		t.Errorf("Missing 2.2.2.2: %s", spew.Sdump(clients))
	}
	if _, ok := clients.clients["1.1.1.1"]; ok {
		t.Errorf("Unexpected client 1.1.1.1: %s", spew.Sdump(clients))
	}

	fm.runOnce()
	fm.runOnce()
	fm.runOnce()
	fm.runOnce()
	if clients.newCalls != 2 {
		t.Errorf("clients.newCalls = %d, want 2", clients.newCalls)
	}

	if _, ok := clients.clients["2.2.2.2"]; !ok {
		t.Errorf("Missing 2.2.2.2: %s", spew.Sdump(clients))
	}
	if _, ok := clients.clients["1.1.1.1"]; ok {
		t.Errorf("Unexpected client 1.1.1.1: %s", spew.Sdump(clients))
	}
}

func startTest() (foreman, *clients) {
	fm := createForeman(db.New(), 1)
	clients := &clients{make(map[string]*fakeClient), 0}
	fm.newClient = func(ip string) (client, error) {
		fc := &fakeClient{clients, ip, pb.MinionConfig{}, pb.ContainerConfig{}}
		clients.clients[ip] = fc
		clients.newCalls++
		return fc, nil
	}
	return fm, clients
}

func init() {
	newToken = func(n int) (string, error) {
		return fmt.Sprintf("%d", n), nil
	}
}

type fakeClient struct {
	clients *clients
	ip      string
	mc      pb.MinionConfig
	cc      pb.ContainerConfig
}

func (fc *fakeClient) setMinion(mc pb.MinionConfig) error {
	fc.mc = mc
	return nil
}

func (fc *fakeClient) getMinion() (pb.MinionConfig, error) {
	return fc.mc, nil
}

func (fc *fakeClient) setContainer(cc pb.ContainerConfig) error {
	fc.cc = cc
	return nil
}

func (fc *fakeClient) Close() {
	delete(fc.clients.clients, fc.ip)
}
