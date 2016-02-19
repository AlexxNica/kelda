package network

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/minion/docker"
	"github.com/NetSys/di/minion/supervisor"
	"github.com/NetSys/di/ovsdb"
)

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
func runWorker(conn db.Conn, dk docker.Client, initialized map[string]struct{}) {
	minions := conn.SelectFromMinion(nil)
	if len(minions) != 1 || minions[0].Role != db.Worker {
		return
	}

	var labels []db.Label
	var containers []db.Container
	conn.Transact(func(view db.Database) error {
		containers = view.SelectFromContainer(func(c db.Container) bool {
			return c.SchedID != "" && c.IP != "" && c.Mac != ""
		})
		labels = view.SelectFromLabel(func(l db.Label) bool {
			return l.IP != ""
		})
		return nil
	})

	// Garbage collect initialized.
	for cid := range initialized {
		exists := false
		for _, c := range containers {
			if c.SchedID == cid {
				exists = true
				break
			}
		}

		if !exists {
			delete(initialized, cid)
		}
	}

	// Initialize new containers.
	var initContainers []db.Container
	for _, dbc := range containers {
		if _, ok := initialized[dbc.SchedID]; ok {
			continue
		}

		err := setupContainer(dk, dbc.SchedID, dbc.IP, dbc.Mac, dbc.Pid)
		if err != nil {
			log.Warning("Failed to setup container %s: %s", dbc.SchedID, err)
		} else {
			initialized[dbc.SchedID] = struct{}{}
			initContainers = append(initContainers, dbc)
		}
	}
	containers = initContainers

	updateIPs(containers, labels)
	updateOpenFlow(dk, containers, labels)
}

func updateIPs(containers []db.Container, labels []db.Label) {
	labelIP := make(map[string]string)
	for _, l := range labels {
		labelIP[l.Label] = l.IP
	}

	for _, dbc := range containers {
		pid := strconv.Itoa(dbc.Pid)
		ip := dbc.IP

		// XXX: On Each loop we're flushing all of the IP addresses away, and
		// replacing them even if we dont' need to.  This really isn't ok,
		// instead we should just add or delete addresses that need to change.

		//Flush the exisisting IP addresses.
		err := sh("/sbin/ip", "netns", "exec", pid,
			"ip", "addr", "flush", "dev", "eth0")
		if err != nil {
			log.Error("Failed to flush IPs: %s", err)
			continue
		}

		// Set the mac address,
		err = sh("/sbin/ip", "netns", "exec", pid, "ip", "link", "set", "dev",
			"eth0", "address", dbc.Mac)
		if err != nil {
			log.Error("Failed to set MAC: %s", err)
			continue
		}

		// Set the ip address.
		err = sh("/sbin/ip", "netns", "exec", pid, "ip", "addr", "add", ip,
			"dev", "eth0")
		if err != nil {
			log.Error("Failed to set IP: %s", err)
			continue
		}

		// Set the default gateway.
		err = sh("/sbin/ip", "netns", "exec", pid, "ip", "route", "add",
			"default", "via", ip)
		if err != nil {
			log.Error("Failed to set default gateway: %s", err)
			continue
		}

		for _, l := range dbc.Labels {
			if ip := labelIP[l]; ip != "" {
				err = sh("/sbin/ip", "netns", "exec", pid, "ip",
					"addr", "add", ip, "dev", "eth0")
				if err != nil {
					log.Error("Failed to set label IP: %s", err)
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
			log.Error("Failed to connect to OVSDB: %s", err)
			return
		}
		defer ovsdb.Close()

		ofDI, err := ovsdb.GetOFPort(peerDI)
		if err != nil {
			log.Error("Failed to get OpenFLow Port: %s", err)
			return
		}

		ofVeth, err := ovsdb.GetOFPort(vethOut)
		if err != nil {
			log.Error("Failed to get OpenFLow Port: %s", err)
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
		args = fmt.Sprintf(args, DIBridge, 5000, ofDI, ofVeth)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		args = "ovs-ofctl add-flow %s priority=%d,table=2,in_port=%d," +
			"actions=output:%d"
		args = fmt.Sprintf(args, DIBridge, 5000, ofVeth, ofDI)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		args = "ovs-ofctl add-flow"
		args += " %s priority=%d,table=0,arp,in_port=%d,actions=output:%d"
		args = fmt.Sprintf(args, DIBridge, 4500, ofVeth, ofDI)
		dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

		/* Catch-all toward OVN */
		args = "ovs-ofctl add-flow %s priority=%d,table=0,in_port=%d," +
			"actions=output:%d"
		args = fmt.Sprintf(args, DIBridge, 0, ofVeth, ofDI)
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
		match := fmt.Sprintf("table=0,dl_dst=%s,nw_dst=%s", LabelMac, ip)
		flow0 := fmt.Sprintf("%s,%s,actions=%s,resubmit(,1)", pri, match, mpa)

		// XXX: Our whole algorithm here is based on blowing away all of the
		// existing flows, and replacing them with new ones.  This is *really*
		// not good, instead we should query what flows exist and only make
		// necessary modifications.
		dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "del-flows", match)
		dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "add-flow", flow0)

		for i, mac := range macs {
			flow1 := fmt.Sprintf("priority=5000,table=1,nw_dst=%s,reg0=%d,"+
				"actions=mod_dl_dst:%s,resubmit(,2)", ip, i, mac)
			dk.Exec(supervisor.Ovsvswitchd, "ovs-ofctl", "add-flow", flow1)
		}
	}
}

func setupContainer(dk docker.Client, id, ip, mac string, pidInt int) error {
	pid := strconv.Itoa(pidInt)

	vethIn, vethOut := veths(id)
	peerBr, peerDI := patchPorts(id)

	// Bind netns to the host.
	netns_src := "/hostproc/" + pid + "/ns/net"
	netns_dst := "/var/run/netns/" + pid
	if err := sh("/bin/ln", "-s", netns_src, netns_dst); err != nil {
		return err
	}

	// Create the veth pair.
	err := sh("/sbin/ip", "link", "add", vethOut, "type", "veth", "peer", "name",
		vethIn)
	if err != nil {
		return err
	}

	// Bring up outer interface.
	err = sh("/sbin/ip", "link", "set", vethOut, "up")
	if err != nil {
		return err
	}

	// Move the vethIn inside the container.
	err = sh("/sbin/ip", "link", "set", vethIn, "netns", pid)
	if err != nil {
		return err
	}

	// Change the name of vethIn to eth0.
	err = sh("/sbin/ip", "netns", "exec", pid, "ip", "link", "set", "dev", vethIn, "name",
		"eth0")
	if err != nil {
		return err
	}

	// Bring up the inner interface.
	err = sh("/sbin/ip", "netns", "exec", pid, "ip", "link", "set", "eth0", "up")
	if err != nil {
		return err
	}

	// Set the mtu to for tunnels.
	err = sh("/sbin/ip", "netns", "exec", pid, "ip", "link", "set", "dev", "eth0",
		"mtu", strconv.Itoa(1450))
	if err != nil {
		return err
	}

	// Create patch port between br-int and di-int
	args := "ovs-vsctl add-port %s %s -- add-port %s %s -- set interface %s"
	args += " type=patch options:peer=%s -- add-port %s %s -- set interface %s"
	args += " external_ids:attached_mac=%s external_ids:iface-id=%s"
	args += " type=patch options:peer=%s"
	args = fmt.Sprintf(args, DIBridge, vethOut, DIBridge, peerDI, peerDI,
		peerBr, OVNBridge, peerBr, peerBr, mac, id, peerDI)
	dk.Exec(supervisor.Ovsvswitchd, strings.Split(args, " ")...)

	return nil
}

func veths(id string) (in, out string) {
	return id[0:15], fmt.Sprintf("%s_c", id[0:13])
}

func patchPorts(id string) (br, di string) {
	return fmt.Sprintf("%s_br", id[0:12]), fmt.Sprintf("%s_di", id[0:12])
}

func sh(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err != nil {
		msg := strings.Join(args, " ")
		msg += "\t" + string(outBuf.Bytes())
		msg += "\t" + string(errBuf.Bytes())
		msg += "\t" + err.Error()
		log.Error(msg)
		return fmt.Errorf("%s failed to execute", args[0])
	}

	return nil
}
