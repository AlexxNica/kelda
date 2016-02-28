package supervisor

import (
	"bufio"
	"fmt"
	"os/exec"
	"reflect"
	"strings"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/join"
	"github.com/NetSys/di/minion/docker"

	log "github.com/Sirupsen/logrus"
)

const (
	Etcd          = "etcd"
	Ovncontroller = "ovn-controller"
	Ovnnorthd     = "ovn-northd"
	Ovsdb         = "ovsdb-server"
	Ovsvswitchd   = "ovs-vswitchd"
	Swarm         = "swarm"
)

var images = map[string]string{
	Etcd:          "quay.io/coreos/etcd:v2.2.4",
	Ovncontroller: "quay.io/netsys/ovn-controller",
	Ovnnorthd:     "quay.io/netsys/ovn-northd",
	Ovsdb:         "quay.io/netsys/ovsdb-server",
	Ovsvswitchd:   "quay.io/netsys/ovs-vswitchd",
	Swarm:         "swarm:1.0.1",
}

const etcdHeartbeatInterval = "500"
const etcdElectionTimeout = "5000"

type supervisor struct {
	conn db.Conn
	dk   docker.Client

	role     db.Role
	etcdIPs  []string
	leaderIP string
	IP       string
	leader   bool
}

func Run(conn db.Conn, dk docker.Client) {
	sv := supervisor{conn: conn, dk: dk}
	go sv.runSystem()
	sv.runApp()
}

// Synchronize locally running "application" containers with the database.
func (sv *supervisor) runApp() {
	for range sv.conn.TriggerTick(10, db.MinionTable, db.ContainerTable).C {
		minions := sv.conn.SelectFromMinion(nil)
		if len(minions) != 1 || minions[0].Role != db.Worker {
			continue
		}

		dkcs, err := sv.dk.List(map[string][]string{
			"label": {docker.SchedulerLabelPair},
		})
		if err != nil {
			log.WithError(err).Error("Failed to list local containers.")
			continue
		}

		var tearDowns []string
		sv.conn.Transact(func(view db.Database) error {
			tearDowns = sv.runAppTransact(view, dkcs)
			return nil
		})

		for _, id := range tearDowns {
			// XXX: Not the place to call TeardownContainer(). It's a fairly
			// extreme violation of modularity.
			TeardownContainer(sv.dk, id)
		}
	}
}

func (sv *supervisor) runAppTransact(view db.Database,
	dkcs_ []docker.Container) []string {

	var tearDowns []string

	score := func(left, right interface{}) int {
		dbc := left.(db.Container)
		dkc := right.(docker.Container)

		if dbc.SchedID != dkc.ID {
			return -1
		}
		return 0
	}
	pairs, dbcs, dkcs := join.Join(view.SelectFromContainer(nil), dkcs_, score)

	for _, iface := range dbcs {
		dbc := iface.(db.Container)

		tearDowns = append(tearDowns, dbc.SchedID)
		view.Remove(dbc)
	}

	for _, dkc := range dkcs {
		pairs = append(pairs, join.Pair{view.InsertContainer(), dkc})
	}

	for _, pair := range pairs {
		dbc := pair.L.(db.Container)
		dkc := pair.R.(docker.Container)

		dbc.SchedID = dkc.ID
		dbc.Pid = dkc.Pid
		dbc.Image = dkc.Image
		dbc.Command = append([]string{dkc.Path}, dkc.Args...)
		view.Commit(dbc)
	}

	return tearDowns
}

// Manage system infrstracture containers that support the application.
func (sv *supervisor) runSystem() {
	for _, image := range images {
		go sv.dk.Pull(image)
	}

	for range sv.conn.Trigger(db.MinionTable, db.EtcdTable).C {
		sv.runSystemOnce()
	}
}

func (sv *supervisor) runSystemOnce() {
	var minion db.Minion
	var etcdRow db.Etcd
	minions := sv.conn.SelectFromMinion(nil)
	etcdRows := sv.conn.SelectFromEtcd(nil)
	if len(minions) == 1 {
		minion = minions[0]
	}
	if len(etcdRows) == 1 {
		etcdRow = etcdRows[0]
	}

	if sv.role == minion.Role &&
		reflect.DeepEqual(sv.etcdIPs, etcdRow.EtcdIPs) &&
		sv.leaderIP == etcdRow.LeaderIP &&
		sv.IP == minion.PrivateIP &&
		sv.leader == etcdRow.Leader {
		return
	}

	if minion.Role != sv.role {
		sv.RemoveAll()
	}

	switch minion.Role {
	case db.Master:
		sv.updateMaster(minion.PrivateIP, etcdRow.EtcdIPs,
			etcdRow.Leader)
	case db.Worker:
		sv.updateWorker(minion.PrivateIP, etcdRow.LeaderIP,
			etcdRow.EtcdIPs)
	}

	sv.role = minion.Role
	sv.etcdIPs = etcdRow.EtcdIPs
	sv.leaderIP = etcdRow.LeaderIP
	sv.IP = minion.PrivateIP
	sv.leader = etcdRow.Leader
}

func (sv *supervisor) updateWorker(IP string, leaderIP string, etcdIPs []string) {
	if !reflect.DeepEqual(sv.etcdIPs, etcdIPs) {
		sv.Remove(Etcd)
	}

	if sv.leaderIP != leaderIP || sv.IP != IP {
		sv.Remove(Swarm)
	}

	sv.run(Etcd, fmt.Sprintf("--initial-cluster=%s", initialClusterString(etcdIPs)),
		"--heartbeat-interval="+etcdHeartbeatInterval,
		"--election-timeout="+etcdElectionTimeout,
		"--proxy=on")

	sv.run(Ovsdb)
	sv.run(Ovsvswitchd)

	if leaderIP == "" || IP == "" {
		return
	}

	sv.run(Swarm, "join", fmt.Sprintf("--addr=%s:2375", IP), "etcd://127.0.0.1:2379")

	minions := sv.conn.SelectFromMinion(nil)
	if len(minions) != 1 {
		return
	}

	err := sv.dk.Exec(Ovsvswitchd, "ovs-vsctl", "set", "Open_vSwitch", ".",
		fmt.Sprintf("external_ids:ovn-remote=\"tcp:%s:6640\"", leaderIP),
		fmt.Sprintf("external_ids:ovn-encap-ip=%s", IP),
		"external_ids:ovn-encap-type=\"geneve\"",
		fmt.Sprintf("external_ids:api_server=\"http://%s:9000\"", leaderIP),
		fmt.Sprintf("external_ids:system-id=\"di-%s\"", minions[0].MinionID),
		"--", "add-br", "di-int",
		"--", "set", "bridge", "di-int", "fail_mode=secure")
	if err != nil {
		log.WithError(err).Warnf("Failed to exec in %s.", Ovsvswitchd)
	}

	/* The ovn controller doesn't support reconfiguring ovn-remote mid-run.
	 * So, we need to restart the container when the leader changes. */
	sv.Remove(Ovncontroller)
	sv.run(Ovncontroller)
}

func (sv *supervisor) updateMaster(IP string, etcdIPs []string, leader bool) {
	if sv.IP != IP || !reflect.DeepEqual(sv.etcdIPs, etcdIPs) {
		sv.Remove(Etcd)
	}

	if sv.IP != IP {
		sv.Remove(Swarm)
	}

	if IP == "" || len(etcdIPs) == 0 {
		return
	}

	sv.run(Etcd, fmt.Sprintf("--name=master-%s", IP),
		fmt.Sprintf("--initial-cluster=%s", initialClusterString(etcdIPs)),
		fmt.Sprintf("--advertise-client-urls=http://%s:2379", IP),
		fmt.Sprintf("--listen-peer-urls=http://%s:2380", IP),
		fmt.Sprintf("--initial-advertise-peer-urls=http://%s:2380", IP),
		"--listen-client-urls=http://0.0.0.0:2379",
		"--heartbeat-interval="+etcdHeartbeatInterval,
		"--initial-cluster-state=new",
		"--election-timeout="+etcdElectionTimeout)
	sv.run(Ovsdb)

	swarmAddr := IP + ":2377"
	sv.run(Swarm, "manage", "--replication", "--addr="+swarmAddr,
		"--host="+swarmAddr, "etcd://127.0.0.1:2379")

	if leader {
		/* XXX: If we fail to boot ovn-northd, we should give up
		* our leadership somehow.  This ties into the general
		* problem of monitoring health. */
		sv.run(Ovnnorthd)
	} else {
		sv.Remove(Ovnnorthd)
	}
}

func (sv *supervisor) run(name string, args ...string) {
	ro := docker.RunOptions{
		Name:        name,
		Image:       images[name],
		Args:        args,
		NetworkMode: "host",
	}

	switch name {
	case Ovsvswitchd:
		ro.Privileged = true
		fallthrough
	case Ovnnorthd:
		fallthrough
	case Ovncontroller:
		ro.VolumesFrom = []string{Ovsdb}
	case Etcd:
		fallthrough
	case Ovsdb:
		ro.Binds = []string{"/usr/share/ca-certificates:/etc/ssl/certs"}
	}

	if err := sv.dk.Run(ro); err != nil {
		log.WithError(err).Warnf("Failed to run %s.", name)
	}
}

func (sv *supervisor) Remove(name string) {
	if err := sv.dk.Remove(name); err != nil {
		log.WithError(err).Warnf("Failed to remove %s.")
	}
}

func (sv *supervisor) RemoveAll() {
	for name := range images {
		sv.Remove(name)
	}
}

func initialClusterString(etcdIPs []string) string {
	var initialCluster []string
	for _, ip := range etcdIPs {
		initialCluster = append(initialCluster, fmt.Sprintf("%s=http://%s:2380", nodeName(ip), ip))
	}
	return strings.Join(initialCluster, ",")
}

func nodeName(IP string) string {
	return fmt.Sprintf("master-%s", IP)
}

// XXX: This is soooo ugly that outsiders call it.  Very important that we fix this.
func TeardownContainer(dk docker.Client, id string) {
	if id == "" {
		return
	}
	veth_outside := id[0:15]
	peer_ovn := fmt.Sprintf("%s_o", id[0:13])
	peer_di := fmt.Sprintf("%s_d", id[0:13])

	// delete veth_outside
	c := exec.Command("/sbin/ip", "link", "delete", veth_outside)
	stderr, _ := c.StderrPipe()
	c.Start()
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		log.Error(sc.Text())
	}
	c.Wait()

	// delete patch ports
	dk.Exec(Ovsvswitchd, "ovs-vsctl", "del-port", veth_outside, "--",
		"del-port", peer_ovn, "--", "del-port", peer_di)
}
