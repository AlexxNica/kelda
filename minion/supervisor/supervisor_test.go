package supervisor

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/minion/docker"
	"github.com/davecgh/go-spew/spew"
)

func TestNone(t *testing.T) {
	ctx := initTest()

	if len(ctx.fd.running) > 0 {
		t.Errorf("fd.running = %s; want <empty>", spew.Sdump(ctx.fd.running))
	}

	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s; want <empty>", spew.Sdump(ctx.fd.exec))
	}

	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.PrivateIP = "1.2.3.4"
		e.Leader = false
		e.LeaderIP = "5.6.7.8"
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	if len(ctx.fd.running) > 0 {
		t.Errorf("fd.running = %s; want <none>", spew.Sdump(ctx.fd.running))
	}

	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s; want <none>", spew.Sdump(ctx.fd.exec))
	}
}

func TestMaster(t *testing.T) {
	ctx := initTest()
	ip := "1.2.3.4"
	etcdIPs := []string{""}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp := map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s\n\nwant <empty>", spew.Sdump(ctx.fd.exec))
	}

	/* Change IP, etcd IPs, and become the leader. */
	ip = "8.8.8.8"
	etcdIPs = []string{"8.8.8.8"}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		e.Leader = true
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:      etcdArgsMaster(ip, etcdIPs),
		Ovsdb:     nil,
		Swarm:     swarmArgsMaster(ip),
		Ovnnorthd: nil,
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s\n\nwant <empty>", spew.Sdump(ctx.fd.exec))
	}

	/* Lose leadership. */
	ctx.conn.Transact(func(view db.Database) error {
		e := view.SelectFromEtcd(nil)[0]
		e.Leader = false
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s\n\nwant <empty>", spew.Sdump(ctx.fd.exec))
	}
}

func TestWorker(t *testing.T) {
	ctx := initTest()
	ip := "1.2.3.4"
	etcdIPs := []string{ip}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Worker
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp := map[string][]string{
		Etcd:        etcdArgsWorker(etcdIPs),
		Ovsdb:       nil,
		Ovsvswitchd: nil,
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s\n\nwant <empty>", spew.Sdump(ctx.fd.exec))
	}

	leaderIP := "5.6.7.8"
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Worker
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		e.LeaderIP = leaderIP
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:          etcdArgsWorker(etcdIPs),
		Ovsdb:         nil,
		Ovncontroller: nil,
		Ovsvswitchd:   nil,
		Swarm:         swarmArgsWorker(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	var mID string
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		mID = m.MinionID
		return nil
	})
	exp = map[string][]string{
		Ovsvswitchd: ovsExecArgs(mID, ip, leaderIP),
	}
	if !reflect.DeepEqual(ctx.fd.exec, exp) {
		t.Errorf("fd.exec = %s\n\nwant %s", spew.Sdump(ctx.fd.exec), spew.Sdump(exp))
	}
}

func TestChange(t *testing.T) {
	ctx := initTest()
	ip := "1.2.3.4"
	leaderIP := "5.6.7.8"
	etcdIPs := []string{ip, leaderIP}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Worker
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		e.LeaderIP = leaderIP
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp := map[string][]string{
		Etcd:          etcdArgsWorker(etcdIPs),
		Ovsdb:         nil,
		Ovncontroller: nil,
		Ovsvswitchd:   nil,
		Swarm:         swarmArgsWorker(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	var mID string
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		mID = m.MinionID
		return nil
	})
	exp = map[string][]string{
		Ovsvswitchd: ovsExecArgs(mID, ip, leaderIP),
	}
	if !reflect.DeepEqual(ctx.fd.exec, exp) {
		t.Errorf("fd.exec = %s\n\nwant %s", spew.Sdump(ctx.fd.exec), spew.Sdump(exp))
	}

	delete(ctx.fd.exec, Ovsvswitchd)
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		m.Role = db.Master
		view.Commit(m)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
	if len(ctx.fd.exec) > 0 {
		t.Errorf("fd.exec = %s\n\nwant <empty>", spew.Sdump(ctx.fd.exec))
	}

	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		m.Role = db.Worker
		view.Commit(m)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:          etcdArgsWorker(etcdIPs),
		Ovsdb:         nil,
		Ovncontroller: nil,
		Ovsvswitchd:   nil,
		Swarm:         swarmArgsWorker(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	exp = map[string][]string{
		Ovsvswitchd: ovsExecArgs(mID, ip, leaderIP),
	}
	if !reflect.DeepEqual(ctx.fd.exec, exp) {
		t.Errorf("fd.exec = %s\n\nwant %s", spew.Sdump(ctx.fd.exec), spew.Sdump(exp))
	}
}

func TestEtcdAdd(t *testing.T) {
	ctx := initTest()
	ip := "1.2.3.4"
	etcdIPs := []string{ip, "5.6.7.8"}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp := map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	// Add a new master
	etcdIPs = append(etcdIPs, "9.10.11.12")
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
}

func TestEtcdRemove(t *testing.T) {
	ctx := initTest()
	ip := "1.2.3.4"
	etcdIPs := []string{ip, "5.6.7.8"}
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		m.PrivateIP = ip
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp := map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}

	// Remove a master
	etcdIPs = etcdIPs[1:]
	ctx.conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		e := view.SelectFromEtcd(nil)[0]
		m.Role = db.Master
		e.EtcdIPs = etcdIPs
		view.Commit(m)
		view.Commit(e)
		return nil
	})
	ctx.run()

	exp = map[string][]string{
		Etcd:  etcdArgsMaster(ip, etcdIPs),
		Ovsdb: nil,
		Swarm: swarmArgsMaster(ip),
	}
	if !reflect.DeepEqual(ctx.fd.running, exp) {
		t.Errorf("fd.running = %s\n\nwant %s", spew.Sdump(ctx.fd.running),
			spew.Sdump(exp))
	}
}

func TestAppDiff(t *testing.T) {
	var expRemove []db.Container
	var expInsert []docker.Container
	var expCommit []db.Container
	remove, insert, commit := diffApp(nil, nil)
	if !eq(remove, expRemove) {
		t.Error(spew.Sprintf("\nremove: %s\nexpected: %s\n", remove, expRemove))
	}
	if !eq(insert, expInsert) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", insert, expInsert))
	}
	if !eq(commit, expCommit) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", commit, expCommit))
	}

	dbcs := []db.Container{{ID: 1}, {ID: 2}}
	expRemove = dbcs
	expInsert = nil
	expCommit = nil
	remove, insert, commit = diffApp(dbcs, nil)
	if !eq(remove, expRemove) {
		t.Error(spew.Sprintf("\nremove: %s\nexpected: %s\n", remove, expRemove))
	}
	if !eq(insert, expInsert) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", insert, expInsert))
	}
	if !eq(commit, expCommit) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", commit, expCommit))
	}

	dkcs := []docker.Container{{ID: "a"}}
	dbcs = nil
	expRemove = nil
	expInsert = dkcs
	expCommit = nil
	remove, insert, commit = diffApp(dbcs, dkcs)
	if !eq(remove, expRemove) {
		t.Error(spew.Sprintf("\nremove: %s\nexpected: %s\n", remove, expRemove))
	}
	if !eq(insert, expInsert) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", insert, expInsert))
	}
	if !eq(commit, expCommit) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", commit, expCommit))
	}

	dkcs = []docker.Container{{ID: "a"}, {ID: "b"}}
	dbcs = []db.Container{{ID: 1, SchedID: "a"}, {ID: 2, SchedID: "c"}}
	expRemove = dbcs[1:]
	expInsert = dkcs[1:]
	expCommit = dbcs[:1]
	remove, insert, commit = diffApp(dbcs, dkcs)
	if !eq(remove, expRemove) {
		t.Error(spew.Sprintf("\nremove: %s\nexpected: %s\n", remove, expRemove))
	}
	if !eq(insert, expInsert) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", insert, expInsert))
	}
	if !eq(commit, expCommit) {
		t.Error(spew.Sprintf("\ninsert: %s\nexpected: %s\n", commit, expCommit))
	}
}

type testCtx struct {
	sv supervisor
	fd fakeDocker

	conn  db.Conn
	trigg db.Trigger
}

func initTest() testCtx {
	conn := db.New()
	ctx := testCtx{supervisor{},
		fakeDocker{make(map[string][]string), make(map[string][]string),
			make(map[string]bool)},
		conn, conn.Trigger(db.MinionTable, db.EtcdTable)}
	ctx.sv.conn = ctx.conn
	ctx.sv.dk = ctx.fd

	ctx.conn.Transact(func(view db.Database) error {
		m := view.InsertMinion()
		m.MinionID = "Minion1"
		view.Commit(m)
		e := view.InsertEtcd()
		view.Commit(e)
		return nil
	})
	ctx.sv.runSystemOnce()

	return ctx
}

func (ctx testCtx) run() {
	select {
	case <-ctx.trigg.C:
	}
	ctx.sv.runSystemOnce()
}

type fakeDocker struct {
	running   map[string][]string
	exec      map[string][]string
	lswitches map[string]bool
}

func (f fakeDocker) Run(opts docker.RunOptions) error {
	validateImage(opts.Name)
	if _, ok := f.running[opts.Name]; ok {
		return nil
	}

	f.running[opts.Name] = opts.Args
	return nil
}

func (f fakeDocker) Exec(image string, cmd ...string) error {
	validateImage(image)
	f.exec[image] = cmd
	return nil
}

func (f fakeDocker) Remove(image string) error {
	validateImage(image)
	delete(f.running, image)
	return nil
}

func (f fakeDocker) RemoveAll() {
	for k := range f.running {
		delete(f.running, k)
	}
}

func (f fakeDocker) Pull(image string) error {
	return nil
}

func (f fakeDocker) RemoveID(id string) error {
	panic("Supervisor does not RemoveID()")
}

func (f fakeDocker) List(filters map[string][]string) ([]docker.Container, error) {
	panic("Supervisor does not List()")
}

func (f fakeDocker) Get(id string) (docker.Container, error) {
	panic("Supervisor does not Get()")
}

func swarmArgsMaster(ip string) []string {
	addr := ip + ":2377"
	return []string{"manage", "--replication", "--addr=" + addr,
		"--host=" + addr, "etcd://127.0.0.1:2379"}
}

func swarmArgsWorker(ip string) []string {
	addr := fmt.Sprintf("--addr=%s:2375", ip)
	return []string{"join", addr, "etcd://127.0.0.1:2379"}
}

func etcdArgsMaster(ip string, etcdIPs []string) []string {
	return []string{
		fmt.Sprintf("--name=master-%s", ip),
		fmt.Sprintf("--initial-cluster=%s", initialClusterString(etcdIPs)),
		fmt.Sprintf("--advertise-client-urls=http://%s:2379", ip),
		fmt.Sprintf("--listen-peer-urls=http://%s:2380", ip),
		fmt.Sprintf("--initial-advertise-peer-urls=http://%s:2380", ip),
		"--listen-client-urls=http://0.0.0.0:2379",
		"--heartbeat-interval=500",
		"--initial-cluster-state=new",
		"--election-timeout=5000",
	}
}

func etcdArgsWorker(etcdIPs []string) []string {
	return []string{
		fmt.Sprintf("--initial-cluster=%s", initialClusterString(etcdIPs)),
		"--heartbeat-interval=500",
		"--election-timeout=5000",
		"--proxy=on",
	}
}

func ovsExecArgs(id, ip, leader string) []string {
	return []string{"ovs-vsctl", "set", "Open_vSwitch", ".",
		fmt.Sprintf("external_ids:ovn-remote=\"tcp:%s:6640\"", leader),
		fmt.Sprintf("external_ids:ovn-encap-ip=%s", ip),
		"external_ids:ovn-encap-type=\"geneve\"",
		fmt.Sprintf("external_ids:api_server=\"http://%s:9000\"", leader),
		fmt.Sprintf("external_ids:system-id=\"di-%s\"", id),
	}
}

func validateImage(image string) {
	switch image {
	case Etcd:
	case Swarm:
	case Ovnnorthd:
	case Ovncontroller:
	case Ovsvswitchd:
	case Ovsdb:
	default:
		panic("Bad Image")
	}
}

func eq(a1, a2 interface{}) bool {
	return reflect.DeepEqual(a1, a2)
}
