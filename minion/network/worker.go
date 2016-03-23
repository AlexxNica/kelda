package network

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/join"
	"github.com/NetSys/di/minion/docker"
	"github.com/NetSys/di/minion/supervisor"
	"github.com/NetSys/di/ovsdb"
	"github.com/NetSys/di/util"

	log "github.com/Sirupsen/logrus"
)

const (
	nsPath    string = "/var/run/netns"
	innerVeth string = "eth0"
	innerMTU  int    = 1450
)

const (
	// ovsPort parameters
	ifaceTypePatch string = "patch"
)

// This represents a network namespace
type nsInfo struct {
	ns  string
	pid int
}

// This represents a network device
type netdev struct {
	// These apply to all links
	name string
	up   bool

	// These only apply to veths
	peerNS  string
	peerMTU int
}

// This represents a OVS port and its default interface
type ovsPort struct {
	name   string
	bridge string

	patch       bool // Is the interface of type patch?
	peer        string
	attachedMAC string
	ifaceID     string
}

// Query the database for any running containers and for each container running on this
// host, do the following: (most of this happens in setupContainer())
//    - Create a pair of virtual interfaces for the container if it's new and
//      assign them the appropriate addresses
//    - Move one of the interfaces into the network namespace of the container,
//      and assign it the MAC and IP addresses from OVN
//    - Attach the other interface to the OVS bridge di-int
//    - Attach this container to the logical network by creating a pair of OVS
//      patch ports between br-int and di-int, then install flows to send traffic
//      between the patch port on di-int and the container's outter interface
//      (These flows live in Table 2)
//    - Update the container's /etc/hosts file with the set of labels it may access.
//    - Populate di-int with the OpenFlow rules necessary to facilitate forwarding.
//
// XXX: The worker additionally has several basic jobs which are currently unimplemented:
//    - ACLS should be installed to guarantee only sanctioned communication.
//    - /etc/hosts in the containers needs to be generated.
func runWorker(conn db.Conn, dk docker.Client) {
	minions := conn.SelectFromMinion(nil)
	if len(minions) != 1 || minions[0].Role != db.Worker {
		return
	}

	var labels []db.Label
	var containers []db.Container
	var connections []db.Connection
	conn.Transact(func(view db.Database) error {
		containers = view.SelectFromContainer(func(c db.Container) bool {
			return c.SchedID != "" && c.IP != "" && c.Mac != ""
		})
		labels = view.SelectFromLabel(func(l db.Label) bool {
			return l.IP != ""
		})
		connections = view.SelectFromConnection(nil)
		return nil
	})

	updateNamespaces(containers)
	updateVeths(containers)
	updatePorts(containers)
	updateIPs(containers, labels)
	updateOpenFlow(dk, containers, labels)
	updateEtcHosts(dk, containers, labels, connections)
}

// If a namespace in the path is detected as invalid and conflicts with
// a namespace that should exist, it's removed and replaced.
func updateNamespaces(containers []db.Container) {
	// A symbolic link in the netns path is considered a "namespace".
	// The actual namespace is elsewhere but we link them all into the
	// canonical location and manage them there.
	//
	// We keep all our namespaces in /var/run/netns/

	var targetNamespaces []nsInfo
	for _, dbc := range containers {
		targetNamespaces = append(targetNamespaces,
			nsInfo{ns: networkNS(dbc.SchedID), pid: dbc.Pid})
	}
	currentNamespaces, err := generateCurrentNamespaces()
	if err != nil {
		log.WithError(err).Error("failed to get namespaces")
		return
	}

	_, lefts, rights := join.Join(currentNamespaces, targetNamespaces, func(
		left, right interface{}) int {
		if left.(nsInfo).ns == right.(nsInfo).ns {
			return 0
		}
		return -1
	})

	for _, l := range lefts {
		if err := delNS(l.(nsInfo)); err != nil {
			log.WithError(err).Error("error deleting namespace")
		}
	}

	for _, r := range rights {
		if err := addNS(r.(nsInfo)); err != nil {
			log.WithError(err).Error("error adding namespace")
		}
	}
}

func generateCurrentNamespaces() ([]nsInfo, error) {
	files, err := ioutil.ReadDir(nsPath)
	if err != nil {
		return nil, err
	}

	var infos []nsInfo
	for _, file := range files {
		fi, err := os.Lstat(fmt.Sprintf("%s/%s", nsPath, file.Name()))
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
			infos = append(infos, nsInfo{ns: file.Name()})
		}
	}
	return infos, nil
}

func delNS(info nsInfo) error {
	netnsDst := fmt.Sprintf("%s/%s", nsPath, info.ns)
	if err := os.Remove(netnsDst); err != nil {
		return fmt.Errorf("failed to remove namespace %s: %s",
			netnsDst, err)
	}
	return nil
}

func addNS(info nsInfo) error {
	netnsSrc := fmt.Sprintf("/hostproc/%d/ns/net", info.pid)
	netnsDst := fmt.Sprintf("%s/%s", nsPath, info.ns)
	if _, err := os.Stat(netnsDst); err == nil {
		if err := os.Remove(netnsDst); err != nil {
			return fmt.Errorf("failed to remove broken namespace %s: %s",
				netnsDst, err)
		}
	} else if !os.IsNotExist(err) && err != nil {
		return fmt.Errorf("failed to query namespace %s: %s",
			netnsDst, err)
	}
	if err := os.Symlink(netnsSrc, netnsDst); err != nil {
		return fmt.Errorf("failed to create namespace %s with source %s: %s",
			netnsDst, netnsSrc, err)
	}
	return nil
}

func updateVeths(containers []db.Container) {
	// A virtual ethernet link that links the host and container is a "veth".
	//
	// The ends of the veth have different config options like mtu, etc.
	// However if you delete one side, both will be deleted.

	targetVeths := generateTargetVeths(containers)
	currentVeths, err := generateCurrentVeths(containers)
	if err != nil {
		log.WithError(err).Error("failed to get veths")
		return
	}

	pairs, lefts, rights := join.Join(currentVeths, targetVeths, func(
		left, right interface{}) int {
		if left.(netdev).name == right.(netdev).name {
			return 0
		}
		return -1
	})

	for _, l := range lefts {
		if err := delVeth(l.(netdev)); err != nil {
			log.WithError(err).Error("failed to delete veth")
			continue
		}
	}
	for _, r := range rights {
		if err := addVeth(r.(netdev)); err != nil {
			log.WithError(err).Error("failed to add veth")
			continue
		}
	}
	for _, p := range pairs {
		if err := modVeth(p.L.(netdev), p.R.(netdev)); err != nil {
			log.WithError(err).Error("failed to modify veth")
			continue
		}
	}
}

func generateTargetVeths(containers []db.Container) []netdev {
	var configs []netdev
	for _, dbc := range containers {
		_, vethOut := veths(dbc.SchedID)
		cfg := netdev{
			name:    vethOut,
			up:      true,
			peerNS:  networkNS(dbc.SchedID),
			peerMTU: innerMTU,
		}
		configs = append(configs, cfg)
	}
	return configs
}

func generateCurrentVeths(containers []db.Container) ([]netdev, error) {
	names, err := listVeths()
	if err != nil {
		return nil, err
	}

	var configs []netdev
	for _, name := range names {
		cfg := netdev{
			name: name,
		}

		iface, err := net.InterfaceByName(name)
		if err != nil {
			log.WithFields(log.Fields{
				"name":  name,
				"error": err,
			}).Error("failed to get interface")
			continue
		}

		for _, dbc := range containers {
			_, vethOut := veths(dbc.SchedID)
			if vethOut == name {
				cfg.peerNS = networkNS(dbc.SchedID)
				break
			}
		}
		if cfg.peerNS != "" {
			if nsExists, err := namespaceExists(cfg.peerNS); err != nil {
				log.WithFields(log.Fields{
					"namespace": cfg.peerNS,
					"error":     err,
				}).Error("error searching for namespace")
				continue
			} else if nsExists {
				lkExists, err := linkExists(cfg.peerNS, innerVeth)
				if err != nil {
					log.WithFields(log.Fields{
						"namespace": cfg.peerNS,
						"link":      innerVeth,
						"error":     err,
					}).Error("error checking if link exists in namespace")
					continue
				} else if lkExists {
					cfg.peerMTU, err = getLinkMTU(cfg.peerNS, innerVeth)
					if err != nil {
						log.WithError(err).Error("failed to get link mtu")
						continue
					}
				}
			}
		}

		cfg.up = (iface.Flags&net.FlagUp == net.FlagUp)
		configs = append(configs, cfg)
	}
	return configs, nil
}

// There certain exceptions, as certain ports will never be deleted.
func updatePorts(containers []db.Container) {
	// An Open vSwitch patch port is referred to as a "port".

	odb, err := ovsdb.Open()
	if err != nil {
		log.WithError(err).Error("failed to connect to OVSDB")
		return
	}
	defer odb.Close()

	targetPorts := generateTargetPorts(containers)
	currentPorts, err := generateCurrentPorts(odb)
	if err != nil {
		log.WithError(err).Error("failed to generate current openflow ports")
		return
	}

	pairs, lefts, rights := join.Join(currentPorts, targetPorts, func(
		left, right interface{}) int {
		if left.(ovsPort).name == right.(ovsPort).name &&
			left.(ovsPort).bridge == right.(ovsPort).bridge {
			return 0
		}
		return -1
	})

	for _, l := range lefts {
		if l.(ovsPort).name == l.(ovsPort).bridge {
			// The "bridge" port for the bridge should never be deleted
			continue
		}
		if err := delPort(odb, l.(ovsPort)); err != nil {
			log.WithError(err).Error("failed to delete openflow port")
			continue
		}
	}
	for _, r := range rights {
		if err := addPort(odb, r.(ovsPort)); err != nil {
			log.WithError(err).Error("failed to add openflow port")
			continue
		}
	}
	for _, p := range pairs {
		if err := modPort(odb, p.L.(ovsPort), p.R.(ovsPort)); err != nil {
			log.WithError(err).Error("failed to modify openflow port")
			continue
		}
	}
}

func generateTargetPorts(containers []db.Container) []ovsPort {
	var configs []ovsPort
	for _, dbc := range containers {
		_, vethOut := veths(dbc.SchedID)
		peerBr, peerDI := patchPorts(dbc.SchedID)
		configs = append(configs, ovsPort{
			name:   vethOut,
			bridge: diBridge,
		})
		configs = append(configs, ovsPort{
			name:   peerDI,
			bridge: diBridge,
			patch:  true,
			peer:   peerBr,
		})
		configs = append(configs, ovsPort{
			name:        peerBr,
			bridge:      ovnBridge,
			patch:       true,
			peer:        peerDI,
			attachedMAC: dbc.Mac,
			ifaceID:     dbc.SchedID,
		})
	}
	return configs
}

func generateCurrentPorts(odb ovsdb.Ovsdb) ([]ovsPort, error) {
	var configs []ovsPort
	for _, bridge := range []string{diBridge, ovnBridge} {
		ports, err := odb.ListOFPorts(bridge)
		if err != nil {
			return nil, fmt.Errorf("error listing ports on bridge %s: %s",
				bridge, err)
		}
		for _, port := range ports {
			cfg, err := populatePortConfig(odb, bridge, port)
			if err != nil {
				return nil, fmt.Errorf("error populating port config: %s", err)
			}
			configs = append(configs, *cfg)
		}
	}
	return configs, nil
}

// XXX This should actually be done at the level of the ovsdb wrapper.
// As in, you should only have to get the row once, and then populate a
// struct with all necessary fields and have them be picked out into here
func populatePortConfig(odb ovsdb.Ovsdb, bridge, port string) (
	*ovsPort, error) {
	config := &ovsPort{
		name:   port,
		bridge: bridge,
	}

	iface, err := odb.GetDefaultOFInterface(port)
	if err != nil {
		return nil, err
	}

	itype, err := odb.GetOFInterfaceType(iface)
	if err != nil && !ovsdb.IsExist(err) {
		return nil, err
	} else if err == nil {
		switch itype {
		case "patch":
			config.patch = true
		}
	}

	peer, err := odb.GetOFInterfacePeer(iface)
	if err != nil && !ovsdb.IsExist(err) {
		return nil, err
	} else if err == nil {
		config.peer = peer
	}

	attachedMAC, err := odb.GetOFInterfaceAttachedMAC(iface)
	if err != nil && !ovsdb.IsExist(err) {
		return nil, err
	} else if err == nil {
		config.attachedMAC = attachedMAC
	}

	ifaceID, err := odb.GetOFInterfaceIfaceID(iface)
	if err != nil && !ovsdb.IsExist(err) {
		return nil, err
	} else if err == nil {
		config.ifaceID = ifaceID
	}
	return config, nil
}

func delPort(odb ovsdb.Ovsdb, config ovsPort) error {
	if err := odb.DeleteOFPort(config.bridge, config.name); err != nil {
		return fmt.Errorf("error deleting openflow port: %s", err)
	}
	return nil
}

func addPort(odb ovsdb.Ovsdb, config ovsPort) error {
	if err := odb.CreateOFPort(config.bridge, config.name); err != nil {
		return fmt.Errorf("error creating openflow port: %s", err)
	}

	dummyPort := ovsPort{name: config.name, bridge: config.bridge}
	if err := modPort(odb, dummyPort, config); err != nil {
		return err
	}
	return nil
}

func modPort(odb ovsdb.Ovsdb, current ovsPort, target ovsPort) error {
	if current.patch != target.patch && target.patch {
		err := odb.SetOFInterfaceType(target.name, ifaceTypePatch)
		if err != nil {
			return fmt.Errorf("error setting interface %s to type %s: %s",
				target.name, ifaceTypePatch, err)
		}
	}

	if current.peer != target.peer && target.peer != "" {
		err := odb.SetOFInterfacePeer(target.name, target.peer)
		if err != nil {
			return fmt.Errorf("error setting interface %s with peer %s: %s",
				target.name, target.peer, err)
		}
	}

	if current.attachedMAC != target.attachedMAC && target.attachedMAC != "" {
		err := odb.SetOFInterfaceAttachedMAC(target.name, target.attachedMAC)
		if err != nil {
			return fmt.Errorf("error setting interface %s with mac %s: %s",
				target.name, target.attachedMAC, err)
		}
	}

	if current.ifaceID != target.ifaceID && target.ifaceID != "" {
		err := odb.SetOFInterfaceIfaceID(target.name, target.ifaceID)
		if err != nil {
			return fmt.Errorf("error setting interface %s with id %s: %s",
				target.name, target.ifaceID, err)
		}
	}
	return nil
}

func updateIPs(containers []db.Container, labels []db.Label) {
	labelIP := make(map[string]string)
	for _, l := range labels {
		labelIP[l.Label] = l.IP
	}

	for _, dbc := range containers {
		var err error

		ns := networkNS(dbc.SchedID)
		ip := dbc.IP

		// XXX: On Each loop we're flushing all of the IP addresses away, and
		// replacing them even if we dont' need to.  This really isn't ok,
		// instead we should just add or delete addresses that need to change.

		//Flush the exisisting IP addresses.
		err = sh("ip netns exec %s ip addr flush dev %s", ns, innerVeth)
		if err != nil {
			log.WithError(err).Error("Failed to flush IPs.")
			continue
		}

		// Set the mac address,
		err = sh("ip netns exec %s ip link set dev %s address %s",
			ns, innerVeth, dbc.Mac)
		if err != nil {
			log.WithError(err).Error("Failed to set MAC.")
			continue
		}

		// Set the ip address.
		err = sh("ip netns exec %s ip addr add %s dev %s", ns, ip, innerVeth)
		if err != nil {
			log.WithError(err).Error("Failed to set IP.")
			continue
		}

		// Set the default gateway.
		err = sh("ip netns exec %s ip route add default via %s", ns, ip)
		if err != nil {
			log.WithError(err).Error("Failed to set default gateway.")
			continue
		}

		for _, l := range dbc.Labels {
			if ip := labelIP[l]; ip != "" {
				err = sh("ip netns exec %s ip addr add %s dev %s",
					ns, ip, innerVeth)
				if err != nil {
					log.WithError(err).Error(
						"Failed to set label IP.")
					continue
				}
			}
		}
	}
}

// Sets up the OpenFlow tables to get packets from containers into the OVN controlled
// bridge.  The Openflow tables are organized as follows.
//
//     - Table 0 will check for packets destined to an ip address of a label with MAC
//     0A:00:00:00:00:00 (obtained by OVN faking out arp) and use the OF mulipath action
//     to balance load packets across n links where n is the number of containers
//     implementing the label.  This result is stored in NXM_NX_REG0. This is done using
//     a symmetric l3/4 hash, so transport connections should remain intact.
//
//     -Table 1 reads NXM_NX_REG0 and changes the destination mac address to one of the
//     MACs of the containers that implement the label
//
// XXX: The multipath action doesn't perform well.  We should migrate away from it
// choosing datapath recirculation instead.
func updateOpenFlow(dk docker.Client, containers []db.Container, labels []db.Label) {
	for _, dbc := range containers {
		_, vethOut := veths(dbc.SchedID)
		_, peerDI := patchPorts(dbc.SchedID)

		ovsdb, err := ovsdb.Open()
		if err != nil {
			log.WithError(err).Error("Failed to connect to OVSDB.")
			return
		}
		defer ovsdb.Close()

		ofDI, err := ovsdb.GetOFPortNo(peerDI)
		if err != nil {
			log.WithError(err).Error("Failed to get OpenFLow Port.")
			return
		}

		ofVeth, err := ovsdb.GetOFPortNo(vethOut)
		if err != nil {
			log.WithError(err).Error("Failed to get OpenFLow Port")
			return
		}

		if ofDI < 0 || ofVeth < 0 {
			log.Warning("Missing OpenFlow port number")
			return
		}

		// XXX: While OVS will automatically detect duplicate flows and refrain
		// from adding them.  We still need to go through and delete flows for
		// old containers that are no longer userful.  Really this whole
		// algorithm needs to be revamped.  Instead we should check what flows
		// are there, compute a diff and fix things up.
		args := "ovs-ofctl add-flow %s priority=%d,table=0,in_port=%d," +
			"actions=output:%d"
		args = fmt.Sprintf(args, diBridge, 5000, ofDI, ofVeth)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		args = "ovs-ofctl add-flow %s priority=%d,table=2,in_port=%d," +
			"actions=output:%d"
		args = fmt.Sprintf(args, diBridge, 5000, ofVeth, ofDI)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		args = "ovs-ofctl add-flow"
		args += " %s priority=%d,table=0,arp,in_port=%d,actions=output:%d"
		args = fmt.Sprintf(args, diBridge, 4500, ofVeth, ofDI)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		/* Catch-all toward OVN */
		args = "ovs-ofctl add-flow %s priority=%d,table=0,in_port=%d," +
			"actions=output:%d"
		args = fmt.Sprintf(args, diBridge, 0, ofVeth, ofDI)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)
	}

	LabelMacs := make(map[string]map[string]struct{})
	for _, dbc := range containers {
		for _, l := range dbc.Labels {
			if _, ok := LabelMacs[l]; !ok {
				LabelMacs[l] = make(map[string]struct{})
			}
			LabelMacs[l][dbc.Mac] = struct{}{}
		}
	}

	for _, label := range labels {
		macs := LabelMacs[label.Label]
		if len(macs) == 0 {
			continue
		}

		n := len(macs)
		lg2n := int(math.Ceil(math.Log2(float64(n))))

		ip := label.IP
		pri := "priority=4000"
		mpa := fmt.Sprintf("multipath(symmetric_l3l4, 0, modulo_n, %d, 0,"+
			" NXM_NX_REG0[0..%d])", n, lg2n)
		match := fmt.Sprintf("table=0,dl_dst=%s,nw_dst=%s", labelMac, ip)
		flow0 := fmt.Sprintf("%s,%s,actions=%s,resubmit(,1)", pri, match, mpa)

		// XXX: Our whole algorithm here is based on blowing away all of the
		// existing flows, and replacing them with new ones.  This is *really*
		// not good, instead we should query what flows exist and only make
		// necessary modifications.
		dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "del-flows", match)
		dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "add-flow", flow0)

		i := 0
		for mac := range macs {
			flow1 := fmt.Sprintf("priority=5000,table=1,nw_dst=%s,reg0=%d,"+
				"actions=mod_dl_dst:%s,resubmit(,2)", ip, i, mac)
			dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "add-flow", flow1)
			i++
		}
	}
}

func updateEtcHosts(dk docker.Client, containers []db.Container, labels []db.Label,
	connections []db.Connection) {

	labelIP := make(map[string]string) /* Map label name to its IP. */
	conns := make(map[string][]string) /* Map label to a list of all labels it connect to. */

	for _, l := range labels {
		labelIP[l.Label] = l.IP
	}

	for _, conn := range connections {
		conns[conn.From] = append(conns[conn.From], conn.To)
	}

	for _, dbc := range containers {
		id := dbc.SchedID

		currHosts, err := dk.GetFromContainer(id, "/etc/hosts")
		if err != nil {
			log.WithError(err).Error("Failed to get /etc/hosts")
			return
		}

		newHosts := generateEtcHosts(dbc, labelIP, conns)

		if newHosts != currHosts {
			err = dk.WriteToContainer(id, newHosts, "/etc", "hosts", 0644)
			if err != nil {
				log.WithError(err).Error("Failed to update /etc/hosts")
			}
		}
	}
}

func generateEtcHosts(dbc db.Container, labelIP map[string]string,
	conns map[string][]string) string {

	type entry struct {
		ip, host string
	}

	localhosts := []entry{
		{"127.0.0.1", "localhost"},
		{"::1", "localhost ip6-localhost ip6-loopback"},
		{"fe00::0", "ip6-localnet"},
		{"ff00::0", "ip6-mcastprefix"},
		{"ff02::1", "ip6-allnodes"},
		{"ff02::2", "ip6-allrouters"},
	}

	if dbc.IP != "" && dbc.SchedID != "" {
		entry := entry{dbc.IP, util.ShortUUID(dbc.SchedID)}
		localhosts = append(localhosts, entry)
	}

	newHosts := make(map[entry]struct{})
	for _, entry := range localhosts {
		newHosts[entry] = struct{}{}
	}

	for _, l := range dbc.Labels {
		for _, toLabel := range conns[l] {
			if ip := labelIP[toLabel]; ip != "" {
				newHosts[entry{ip, toLabel + ".di"}] = struct{}{}
			}
		}
	}

	var hosts []string
	for h := range newHosts {
		hosts = append(hosts, fmt.Sprintf("%-15s %s", h.ip, h.host))
	}

	sort.Strings(hosts)
	return strings.Join(hosts, "\n") + "\n"
}

func namespaceExists(namespace string) (bool, error) {
	nsFullPath := fmt.Sprintf("%s/%s", nsPath, namespace)
	file, err := os.Lstat(nsFullPath)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("error finding file %s: %s", nsFullPath, err)
	}

	if file.Mode()&os.ModeSymlink != os.ModeSymlink {
		return false, nil
	}

	if dst, err := os.Readlink(nsFullPath); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("error finding destination of symlink %s: %s",
			nsFullPath, err)
	} else if dst == fmt.Sprintf("/hostproc/%s/ns/net", namespace) {
		return true, nil
	} else {
	}
	return false, nil
}

func networkNS(id string) string {
	return fmt.Sprintf("%s_ns", id[0:13])
}

func veths(id string) (in, out string) {
	return fmt.Sprintf("%s_i", id[0:13]), fmt.Sprintf("%s_c", id[0:13])
}

// Generate the temporary internal veth name from the name of the
// external veth
func tempVethPairName(out string) (in string) {
	return fmt.Sprintf("%s_i", out[0:13])
}

func patchPorts(id string) (br, di string) {
	return fmt.Sprintf("%s_br", id[0:12]), fmt.Sprintf("%s_di", id[0:12])
}

func ipExec(namespace, format string, args ...interface{}) error {
	_, _, err := ipExecVerbose(namespace, format, args...)
	return err
}

// Use like the `ip` command
//
// For example, if you wanted the stats on `eth0` (as in the command
// `ip link show eth0`) then you would pass in ("", "link show %s", "eth0")
//
// If you wanted to run this in namespace `ns1` then you would use
// ("ns1", "link show %s", "eth0")
func ipExecVerbose(namespace, format string, args ...interface{}) (
	stdout, stderr []byte, err error) {
	cmd := fmt.Sprintf(format, args...)
	cmd = fmt.Sprintf("ip %s", cmd)
	if namespace != "" {
		cmd = fmt.Sprintf("ip netns exec %s %s", namespace, cmd)
	}
	return shVerbose(cmd)
}

func sh(format string, args ...interface{}) error {
	_, _, err := shVerbose(format, args...)
	return err
}

// Returns (Stdout, Stderr, error)
//
// It's critical that the error returned here is the exact error
// from "os/exec" commands
func shVerbose(format string, args ...interface{}) (
	stdout, stderr []byte, err error) {
	command := fmt.Sprintf(format, args...)
	cmdArgs := strings.Split(command, " ")
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return nil, nil, err
	}

	return outBuf.Bytes(), errBuf.Bytes(), nil
}

// For debug messages
func namespaceName(namespace string) string {
	if namespace == "" {
		return "root namespace"
	}
	return fmt.Sprintf("%s namespace", namespace)
}
