package cluster

import (
	"testing"
	"time"

	"github.com/NetSys/quilt/cluster/acl"
	"github.com/NetSys/quilt/cluster/machine"
	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/stitch"
	"github.com/stretchr/testify/assert"
)

var FakeAmazon db.Provider = "FakeAmazon"
var FakeVagrant db.Provider = "FakeVagrant"
var amazonCloudConfig = "Amazon Cloud Config"
var vagrantCloudConfig = "Vagrant Cloud Config"

type providerRequest struct {
	request  machine.Machine
	provider provider
	boot     bool
}

type bootRequest struct {
	size        string
	cloudConfig string
}

type fakeProvider struct {
	machines    map[string]machine.Machine
	idCounter   int
	cloudConfig string

	bootRequests []bootRequest
	stopRequests []string
	aclRequests  []acl.ACL
}

func newFakeProvider(cloudConfig string) *fakeProvider {
	var ret fakeProvider
	ret.machines = make(map[string]machine.Machine)
	ret.cloudConfig = cloudConfig
	ret.clearLogs()
	return &ret
}

func (p *fakeProvider) clearLogs() {
	p.bootRequests = []bootRequest{}
	p.stopRequests = []string{}
	p.aclRequests = []acl.ACL{}
}

func (p *fakeProvider) List() ([]machine.Machine, error) {
	var machines []machine.Machine
	for _, machine := range p.machines {
		machines = append(machines, machine)
	}
	return machines, nil
}

func (p *fakeProvider) Boot(bootSet []machine.Machine) error {
	for _, bootSet := range bootSet {
		p.idCounter++
		bootSet.ID = string(p.idCounter)
		p.machines[string(p.idCounter)] = bootSet
		p.bootRequests = append(p.bootRequests, bootRequest{size: bootSet.Size,
			cloudConfig: p.cloudConfig})
	}

	return nil
}

func (p *fakeProvider) Stop(machines []machine.Machine) error {
	for _, machine := range machines {
		delete(p.machines, machine.ID)
		p.stopRequests = append(p.stopRequests, machine.ID)
	}
	return nil
}

func (p *fakeProvider) SetACLs(acls []acl.ACL) error {
	p.aclRequests = acls
	return nil
}

func (p *fakeProvider) Connect(namespace string) error { return nil }

func (p *fakeProvider) ChooseSize(ram stitch.Range, cpu stitch.Range,
	maxPrice float64) string {
	return ""
}

func newTestCluster() cluster {
	conn := db.New()
	clst := cluster{
		conn:      conn,
		providers: make(map[db.Provider]provider),
	}

	clst.providers[FakeAmazon] = newFakeProvider(amazonCloudConfig)
	clst.providers[FakeVagrant] = newFakeProvider(vagrantCloudConfig)

	sleep = func(t time.Duration) {}
	return clst
}

func TestPanicBadProvider(t *testing.T) {
	temp := allProviders
	defer func() {
		r := recover()
		assert.NotNil(t, r)
		allProviders = temp
	}()
	allProviders = []db.Provider{FakeAmazon}
	conn := db.New()
	newCluster(conn, "test")
}

func TestSyncDB(t *testing.T) {
	checkSyncDB := func(cloudMachines []machine.Machine,
		databaseMachines []db.Machine, expectedBoot,
		expectedStop []machine.Machine) {
		_, bootResult, stopResult := syncDB(cloudMachines, databaseMachines)
		assert.Equal(t, expectedBoot, bootResult)
		assert.Equal(t, expectedStop, stopResult)
	}

	var noMachines []machine.Machine
	dbNoSize := db.Machine{Provider: FakeAmazon}
	cmNoSize := machine.Machine{Provider: FakeAmazon}
	dbLarge := db.Machine{Provider: FakeAmazon, Size: "m4.large"}
	cmLarge := machine.Machine{Provider: FakeAmazon, Size: "m4.large"}

	// Test boot with no size
	checkSyncDB(noMachines, []db.Machine{dbNoSize}, []machine.Machine{cmNoSize},
		noMachines)

	// Test boot with size
	checkSyncDB(noMachines, []db.Machine{dbLarge}, []machine.Machine{cmLarge},
		noMachines)

	// Test mixed boot
	checkSyncDB(noMachines, []db.Machine{dbNoSize, dbLarge}, []machine.Machine{
		cmNoSize, cmLarge}, noMachines)

	// Test partial boot
	checkSyncDB([]machine.Machine{cmNoSize}, []db.Machine{dbNoSize, dbLarge},
		[]machine.Machine{cmLarge}, noMachines)

	// Test stop
	checkSyncDB([]machine.Machine{cmNoSize}, []db.Machine{}, noMachines,
		[]machine.Machine{cmNoSize})

	// Test partial stop
	checkSyncDB([]machine.Machine{cmNoSize, cmLarge}, []db.Machine{}, noMachines,
		[]machine.Machine{cmNoSize, cmLarge})
}

func TestSync(t *testing.T) {
	checkSync := func(clst cluster, provider db.Provider, expectedBoot []bootRequest,
		expectedStop []string) {
		clst.sync()
		providerInst := clst.providers[provider].(*fakeProvider)
		bootResult := providerInst.bootRequests
		stopResult := providerInst.stopRequests
		providerInst.clearLogs()
		assert.Equal(t, expectedBoot, bootResult)
		assert.Equal(t, expectedStop, stopResult)
	}

	noBoots := []bootRequest{}
	noStops := []string{}
	amazonLargeBoot := bootRequest{size: "m4.large", cloudConfig: amazonCloudConfig}
	amazonXLargeBoot := bootRequest{size: "m4.xlarge", cloudConfig: amazonCloudConfig}
	vagrantLargeBoot := bootRequest{size: "vagrant.large",
		cloudConfig: vagrantCloudConfig}

	// Test initial boot
	clst := newTestCluster()
	clst.conn.Transact(func(view db.Database) error {
		m := view.InsertMachine()
		m.Role = db.Master
		m.Provider = FakeAmazon
		m.Size = "m4.large"
		view.Commit(m)

		return nil
	})
	checkSync(clst, FakeAmazon, []bootRequest{amazonLargeBoot}, noStops)

	// Test adding a machine with the same provider
	clst.conn.Transact(func(view db.Database) error {
		m := view.InsertMachine()
		m.Role = db.Master
		m.Provider = FakeAmazon
		m.Size = "m4.xlarge"
		view.Commit(m)

		return nil
	})
	checkSync(clst, FakeAmazon, []bootRequest{amazonXLargeBoot}, noStops)

	// Test adding a machine with a different provider
	clst.conn.Transact(func(view db.Database) error {
		m := view.InsertMachine()
		m.Role = db.Master
		m.Provider = FakeVagrant
		m.Size = "vagrant.large"
		view.Commit(m)

		return nil
	})
	checkSync(clst, FakeVagrant, []bootRequest{vagrantLargeBoot}, noStops)

	// Test removing a machine
	var toRemove db.Machine
	clst.conn.Transact(func(view db.Database) error {
		toRemove = view.SelectFromMachine(func(m db.Machine) bool {
			return m.Provider == FakeAmazon && m.Size == "m4.xlarge"
		})[0]
		view.Remove(toRemove)
		return nil
	})
	checkSync(clst, FakeAmazon, noBoots, []string{toRemove.CloudID})

	// Test removing and adding a machine
	clst.conn.Transact(func(view db.Database) error {
		toRemove = view.SelectFromMachine(func(m db.Machine) bool {
			return m.Provider == FakeAmazon && m.Size == "m4.large"
		})[0]
		view.Remove(toRemove)

		m := view.InsertMachine()
		m.Role = db.Worker
		m.Provider = FakeAmazon
		m.Size = "m4.xlarge"
		view.Commit(m)

		return nil
	})
	checkSync(clst, FakeAmazon, []bootRequest{amazonXLargeBoot},
		[]string{toRemove.CloudID})
}

func TestACLs(t *testing.T) {
	myIP = func() (string, error) {
		return "5.6.7.8", nil
	}

	clst := newTestCluster()
	clst.syncACLs([]string{"admin"},
		[]db.PortRange{
			{
				MinPort: 80,
				MaxPort: 80,
			},
		},
		[]db.Machine{
			{
				Provider: FakeAmazon,
				PublicIP: "8.8.8.8",
			},
			{},
		},
	)

	exp := []acl.ACL{
		{
			CidrIP:  "admin",
			MinPort: 1,
			MaxPort: 65535,
		},
		{
			CidrIP:  "5.6.7.8/32",
			MinPort: 1,
			MaxPort: 65535,
		},
		{
			CidrIP:  "0.0.0.0/0",
			MinPort: 80,
			MaxPort: 80,
		},
		{
			CidrIP:  "8.8.8.8/32",
			MinPort: 1,
			MaxPort: 65535,
		},
	}
	actual := clst.providers[FakeAmazon].(*fakeProvider).aclRequests
	assert.Equal(t, exp, actual)
}
