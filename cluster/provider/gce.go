package provider

////// SET UP API ACCESS:
//
// 1) In the Google Developer Console navigate to:
//    Permissions > Service accounts
//
// 2) Create or use an existing Service Account
//
// 3) For your Service Account, create and save a key as "~/.gce/quilt.json"
//
// 4) In the Google Developer Console navigate to:
//    Permissions > Permissions
//
// 5) If the Service Account is not already, assign it the "Editor" role.
//    You select the account by email.

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/NetSys/quilt/constants"
	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/join"
	"github.com/NetSys/quilt/stitch"

	log "github.com/Sirupsen/logrus"
	"github.com/satori/go.uuid"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
)

const computeBaseURL string = "https://www.googleapis.com/compute/v1/projects"
const (
	// These are the various types of Operations that the GCE API returns
	local = iota
	global
)

var supportedZones = []string{"us-central1-a", "us-east1-b", "europe-west1-b"}

var gAuthClient *http.Client    // the oAuth client
var gceService *compute.Service // gce service

type gceCluster struct {
	projID    string // gce project ID
	imgURL    string // gce url to the VM image
	baseURL   string // gce project specific url prefix
	ipv4Range string // ipv4 range of the internal network
	intFW     string // gce internal firewall name

	ns string // cluster namespace
	id int    // the id of the cluster, used externally
}

// Create a GCE cluster.
//
// Clusters are differentiated (namespace) by setting the description and
// filtering off of that.
//
// XXX: A lot of the fields are hardcoded.
func (clst *gceCluster) Connect(namespace string) error {
	if err := gceInit(); err != nil {
		log.WithError(err).Debug("failed to start up gce")
		return err
	}

	clst.projID = "declarative-infrastructure"
	clst.ns = namespace
	clst.imgURL = fmt.Sprintf(
		"%s/%s",
		computeBaseURL,
		"ubuntu-os-cloud/global/images/ubuntu-1604-xenial-v20160921")
	clst.baseURL = fmt.Sprintf("%s/%s", computeBaseURL, clst.projID)
	clst.ipv4Range = "192.168.0.0/16"
	clst.intFW = fmt.Sprintf("%s-internal", clst.ns)

	if err := clst.netInit(); err != nil {
		log.WithError(err).Debug("failed to start up gce network")
		return err
	}

	if err := clst.fwInit(); err != nil {
		log.WithError(err).Debug("failed to start up gce firewalls")
		return err
	}

	return nil
}

// Get a list of machines from the cluster
//
// XXX: This doesn't use the instance group listing functionality because
// listing that way doesn't get you information about the instances
func (clst *gceCluster) List() ([]Machine, error) {
	var mList []Machine
	for _, zone := range supportedZones {
		list, err := gceService.Instances.List(clst.projID, zone).
			Filter(fmt.Sprintf("description eq %s", clst.ns)).Do()
		if err != nil {
			return nil, err
		}
		for _, item := range list.Items {
			// XXX: This make some iffy assumptions about NetworkInterfaces
			machineSplitURL := strings.Split(item.MachineType, "/")
			mtype := machineSplitURL[len(machineSplitURL)-1]
			mList = append(mList, Machine{
				ID: item.Name,
				PublicIP: item.NetworkInterfaces[0].
					AccessConfigs[0].NatIP,
				PrivateIP: item.NetworkInterfaces[0].NetworkIP,
				Size:      mtype,
				Region:    zone,
				Provider:  db.Google,
			})
		}
	}
	return mList, nil
}

// Boots instances, it is a blocking call.
//
// XXX: currently ignores cloudConfig
// XXX: should probably have a better clean up routine if an error is encountered
func (clst *gceCluster) Boot(bootSet []Machine) error {
	var names []string
	for _, m := range bootSet {
		name := "quilt-" + uuid.NewV4().String()
		_, err := clst.instanceNew(name, m.Size, m.Region,
			cloudConfigUbuntu(m.SSHKeys, "xenial"))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"id":    m.ID,
			}).Error("Failed to start instance.")
			continue
		}
		names = append(names, name)
	}
	if err := clst.wait(names, true); err != nil {
		return err
	}
	return nil
}

// Deletes the instances, it is a blocking call.
//
// If an error occurs while deleting, it will finish the ones that have
// successfully started before returning.
//
// XXX: should probably have a better clean up routine if an error is encountered
func (clst *gceCluster) Stop(machines []Machine) error {
	var names []string
	for _, m := range machines {
		if _, err := clst.instanceDel(m.ID, m.Region); err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"id":    m.ID,
			}).Error("Failed to delete instance.")
			continue
		}
		names = append(names, m.ID)
	}
	if err := clst.wait(names, false); err != nil {
		return err
	}
	return nil
}

func (clst *gceCluster) ChooseSize(ram stitch.Range, cpu stitch.Range,
	maxPrice float64) string {
	return pickBestSize(constants.GoogleDescriptions, ram, cpu, maxPrice)
}

// Get() and operationWait() don't always present the same results, so
// Boot() and Stop() must have a special wait to stay in sync with Get().
func (clst *gceCluster) wait(names []string, live bool) error {
	if len(names) == 0 {
		return nil
	}

	after := time.After(3 * time.Minute)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	for range tick.C {
		select {
		case <-after:
			return errors.New("wait(): timeout")
		default:
		}

		for len(names) > 0 {
			name := names[0]
			instances, err := clst.List()
			if err != nil {
				return err
			}
			exists := false
			for _, ist := range instances {
				if name == ist.ID {
					exists = true
				}
			}
			if live == exists {
				names = append(names[:0], names[1:]...)
			}
		}
		if len(names) == 0 {
			return nil
		}
	}
	return nil
}

// Blocking wait with a hardcoded timeout.
//
// Waits on operations, the type of which is indicated by 'domain'. All
// operations must be of the same 'domain'
//
// XXX: maybe not hardcode timeout, and retry interval
func (clst *gceCluster) operationWait(ops []*compute.Operation, domain int) error {
	if len(ops) == 0 {
		return nil
	}

	after := time.After(3 * time.Minute)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	var op *compute.Operation
	var err error
	for {
		select {
		case <-after:
			return fmt.Errorf("operationWait(): timeout")
		case <-tick.C:
			for len(ops) > 0 {
				switch {
				case domain == local:
					op, err = gceService.ZoneOperations.
						Get(clst.projID, ops[0].Zone,
							ops[0].Name).Do()
				case domain == global:
					op, err = gceService.GlobalOperations.
						Get(clst.projID, ops[0].Name).Do()
				}
				if err != nil {
					return err
				}
				if op.Status != "DONE" {
					break
				}
				ops = append(ops[:0], ops[1:]...)
			}
			if len(ops) == 0 {
				return nil
			}
		}
	}
}

// Get a GCE instance.
func (clst *gceCluster) instanceGet(name, zone string) (*compute.Instance, error) {
	ist, err := gceService.Instances.
		Get(clst.projID, zone, name).Do()
	return ist, err
}

// Create new GCE instance.
//
// Does not check if the operation succeeds.
//
// XXX: all kinds of hardcoded junk in here
// XXX: currently only defines the bare minimum
func (clst *gceCluster) instanceNew(name string, size string, zone string,
	cloudConfig string) (*compute.Operation, error) {
	instance := &compute.Instance{
		Name:        name,
		Description: clst.ns,
		MachineType: fmt.Sprintf("%s/zones/%s/machineTypes/%s",
			clst.baseURL,
			zone,
			size),
		Disks: []*compute.AttachedDisk{
			{
				Boot:       true,
				AutoDelete: true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: clst.imgURL,
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				AccessConfigs: []*compute.AccessConfig{
					{
						Type: "ONE_TO_ONE_NAT",
						Name: "External NAT",
					},
				},
				Network: fmt.Sprintf("%s/global/networks/%s",
					clst.baseURL,
					clst.ns),
			},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "startup-script",
					Value: &cloudConfig,
				},
			},
		},
	}

	op, err := gceService.Instances.
		Insert(clst.projID, zone, instance).Do()
	if err != nil {
		return nil, err
	}
	return op, nil
}

// Delete a GCE instance.
//
// Does not check if the operation succeeds
func (clst *gceCluster) instanceDel(name, zone string) (*compute.Operation, error) {
	op, err := gceService.Instances.Delete(clst.projID, zone, name).Do()
	return op, err
}

func (clst *gceCluster) parseACLs(fws []*compute.Firewall) (acls []ACL) {
	for _, fw := range fws {
		if fw.Name == clst.intFW {
			continue
		}
		for _, cidrIP := range fw.SourceRanges {
			for _, allowed := range fw.Allowed {
				for _, portsStr := range allowed.Ports {
					for _, ports := range strings.Split(
						portsStr, ",") {

						portRange := strings.Split(ports, "-")
						var minPort, maxPort int
						switch len(portRange) {
						case 0:
							minPort, maxPort = 1, 65535
						case 1:
							port, _ := strconv.Atoi(
								portRange[0])
							minPort, maxPort = port, port
						default:
							minPort, _ = strconv.Atoi(
								portRange[0])
							maxPort, _ = strconv.Atoi(
								portRange[1])
						}
						acls = append(acls, ACL{
							CidrIP:  cidrIP,
							MinPort: minPort,
							MaxPort: maxPort,
						})
					}
				}
			}
		}
	}

	return acls
}

func (clst *gceCluster) SetACLs(acls []ACL) error {
	list, err := gceService.Firewalls.List(clst.projID).Do()
	if err != nil {
		return err
	}

	currACLs := clst.parseACLs(list.Items)
	pair, toAdd, toRemove := join.HashJoin(ACLSlice(acls), ACLSlice(currACLs),
		nil, nil)

	var toSet []ACL
	for _, acl := range toAdd {
		toSet = append(toSet, acl.(ACL))
	}
	for _, p := range pair {
		toSet = append(toSet, p.L.(ACL))
	}
	for _, acl := range toRemove {
		toSet = append(toSet, ACL{
			MinPort: acl.(ACL).MinPort,
			MaxPort: acl.(ACL).MaxPort,
			CidrIP:  "", // Remove all currently allowed IPs.
		})
	}

	for acl, cidrIPs := range groupACLsByPorts(toSet) {
		fw, err := clst.getCreateFirewall(acl.MinPort, acl.MaxPort)
		if err != nil {
			return err
		}

		if reflect.DeepEqual(fw.SourceRanges, cidrIPs) {
			continue
		}

		var op *compute.Operation
		if len(cidrIPs) == 0 {
			log.WithField("ports", fmt.Sprintf(
				"%d-%d", acl.MinPort, acl.MaxPort)).
				Debug("Google: Deleting firewall")
			op, err = clst.firewallDelete(fw.Name)
			if err != nil {
				return err
			}
		} else {
			log.WithField("ports", fmt.Sprintf(
				"%d-%d", acl.MinPort, acl.MaxPort)).
				WithField("CidrIPs", cidrIPs).
				Debug("Google: Setting ACLs")
			op, err = clst.firewallPatch(fw.Name, cidrIPs)
			if err != nil {
				return err
			}
		}
		if err := clst.operationWait(
			[]*compute.Operation{op}, global); err != nil {
			return err
		}
	}

	return nil
}

func (clst *gceCluster) getFirewall(name string) (*compute.Firewall, error) {
	list, err := gceService.Firewalls.List(clst.projID).Do()
	if err != nil {
		return nil, err
	}
	for _, val := range list.Items {
		if val.Name == name {
			return val, nil
		}
	}

	return nil, nil
}

func (clst *gceCluster) getCreateFirewall(minPort int, maxPort int) (
	*compute.Firewall, error) {

	ports := fmt.Sprintf("%d-%d", minPort, maxPort)
	fwName := fmt.Sprintf("%s-%s", clst.ns, ports)

	if fw, _ := clst.getFirewall(fwName); fw != nil {
		return fw, nil
	}

	log.WithField("name", fwName).Debug("Creating firewall")
	op, err := clst.insertFirewall(fwName, ports, []string{"127.0.0.1/32"})
	if err != nil {
		return nil, err
	}

	if err := clst.operationWait([]*compute.Operation{op}, global); err != nil {
		return nil, err
	}

	return clst.getFirewall(fwName)
}

// Creates the network for the cluster.
func (clst *gceCluster) networkNew(name string) (*compute.Operation, error) {
	network := &compute.Network{
		Name:      name,
		IPv4Range: clst.ipv4Range,
	}

	op, err := gceService.Networks.Insert(clst.projID, network).Do()
	return op, err
}

func (clst *gceCluster) networkExists(name string) (bool, error) {
	list, err := gceService.Networks.List(clst.projID).Do()
	if err != nil {
		return false, err
	}
	for _, val := range list.Items {
		if val.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// This creates a firewall but does nothing else
//
// XXX: Assumes there is only one network
func (clst *gceCluster) insertFirewall(name, ports string, sourceRanges []string) (
	*compute.Operation, error) {
	firewall := &compute.Firewall{
		Name: name,
		Network: fmt.Sprintf("%s/global/networks/%s",
			clst.baseURL,
			clst.ns),
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{ports},
			},
			{
				IPProtocol: "udp",
				Ports:      []string{ports},
			},
			{
				IPProtocol: "icmp",
			},
		},
		SourceRanges: sourceRanges,
	}

	op, err := gceService.Firewalls.Insert(clst.projID, firewall).Do()
	return op, err
}

func (clst *gceCluster) firewallExists(name string) (bool, error) {
	fw, err := clst.getFirewall(name)
	return fw != nil, err
}

// Updates the firewall using PATCH semantics.
//
// The IP addresses must be in CIDR notation.
// XXX: Assumes there is only one network
// XXX: Assumes the firewall only needs to adjust the IP addrs affected
func (clst *gceCluster) firewallPatch(name string,
	ips []string) (*compute.Operation, error) {
	firewall := &compute.Firewall{
		Name: name,
		Network: fmt.Sprintf("%s/global/networks/%s",
			clst.baseURL,
			clst.ns),
		SourceRanges: ips,
	}

	op, err := gceService.Firewalls.Patch(clst.projID, name, firewall).Do()
	return op, err
}

// Deletes the given firewall
func (clst *gceCluster) firewallDelete(name string) (*compute.Operation, error) {
	return gceService.Firewalls.Delete(clst.projID, name).Do()
}

// Initialize GCE.
//
// Authenication and the client are things that are re-used across clusters.
//
// Idempotent, can call multiple times but will only initialize once.
//
// XXX: ^but should this be the case? maybe we can just have the user call it?
func gceInit() error {
	if gAuthClient == nil {
		log.Debug("GCE initializing...")
		keyfile := filepath.Join(
			os.Getenv("HOME"),
			".gce",
			"quilt.json")
		err := os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", keyfile)
		if err != nil {
			return err
		}
		srv, err := newComputeService(context.Background())
		if err != nil {
			return err
		}
		gceService = srv
	} else {
		log.Debug("GCE already initialized! Skipping...")
	}
	log.Debug("GCE initialize success")
	return nil
}

func newComputeService(ctx context.Context) (*compute.Service, error) {
	client, err := google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		return nil, err
	}
	computeService, err := compute.New(client)
	if err != nil {
		return nil, err
	}
	return computeService, nil
}

// Initializes the network for the cluster
//
// XXX: Currently assumes that each cluster is entirely behind 1 network
func (clst *gceCluster) netInit() error {
	exists, err := clst.networkExists(clst.ns)
	if err != nil {
		return err
	}

	if exists {
		log.Debug("Network already exists")
		return nil
	}

	log.Debug("Creating network")
	op, err := clst.networkNew(clst.ns)
	if err != nil {
		return err
	}

	err = clst.operationWait([]*compute.Operation{op}, global)
	if err != nil {
		return err
	}
	return nil
}

// Initializes the firewall for the cluster
//
// XXX: Currently assumes that each cluster is entirely behind 1 network
func (clst *gceCluster) fwInit() error {
	var ops []*compute.Operation

	if exists, err := clst.firewallExists(clst.intFW); err != nil {
		return err
	} else if exists {
		log.Debug("internal firewall already exists")
	} else {
		log.Debug("creating internal firewall")
		op, err := clst.insertFirewall(
			clst.intFW, "1-65535", []string{clst.ipv4Range})
		if err != nil {
			return err
		}
		ops = append(ops, op)
	}

	if err := clst.operationWait(ops, global); err != nil {
		return err
	}
	return nil
}

func groupACLsByPorts(acls []ACL) map[ACL][]string {
	grouped := make(map[ACL][]string)
	for _, acl := range acls {
		key := ACL{
			MinPort: acl.MinPort,
			MaxPort: acl.MaxPort,
		}
		if _, ok := grouped[key]; !ok {
			grouped[key] = nil
		}
		if acl.CidrIP != "" {
			grouped[key] = append(grouped[key], acl.CidrIP)
		}
	}
	return grouped
}
