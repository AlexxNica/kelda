package cluster

import (
	"github.com/NetSys/di/db"
	"github.com/NetSys/di/util"
	log "github.com/Sirupsen/logrus"
)

type machine struct {
	id        string
	publicIP  string
	privateIP string
}

type provider interface {
	get() ([]machine, error)

	boot(count int, cloudConfig string) error

	stop(ids []string) error

	disconnect()
}

type cluster struct {
	provider
	id          int
	conn        db.Conn
	cloudConfig string
	trigger     db.Trigger
	fm          foreman

	mark bool /* For mark and sweep garbage collection. */
}

// Run continually checks 'conn' for new clusters and implements the policies they
// dictate.
func Run(conn db.Conn) {
	clusters := make(map[int]*cluster)

	for range conn.TriggerTick(60, db.ClusterTable).C {
		var dbClusters []db.Cluster
		conn.Transact(func(db db.Database) error {
			dbClusters = db.SelectFromCluster(nil)
			return nil
		})

		/* Mark and sweep garbage collect the clusters. */
		for _, row := range dbClusters {
			clst, ok := clusters[row.ID]
			if !ok {
				clst = newCluster(conn, row.ID, row.Provider,
					row.Namespace, row.SSHKeys)
				clusters[row.ID] = clst
			}

			clst.mark = true
		}

		for k, clst := range clusters {
			if clst.mark {
				clst.mark = false
			} else {
				clst.fm.stop()
				clst.trigger.Stop()
				delete(clusters, k)
			}
		}
	}
}

func newCluster(conn db.Conn, id int, dbp db.Provider, namespace string,
	keys []string) *cluster {

	var cloud provider
	var err error
	var cloudConfig string

	switch dbp {
	case db.AmazonSpot:
		cloud = newAWS(conn, id, namespace)
		cloudConfig = util.CloudConfigUbuntu(keys)
	case db.Google:
		// XXX: not sure what to do with the error here
		cloud, err = newGCE(conn, id, namespace)
		if err != nil {
			log.WithError(err).Error("Failed to create GCE cluster.")
		}
		cloudConfig = util.CloudConfigCoreOS(keys)
	case db.Azure:
		cloud, err = newAzure(conn, id, namespace)
		if err != nil {
			log.WithError(err).Error("Failed to create Azure cluster.")
		}
		cloudConfig = util.CloudConfigUbuntu(keys)
	case db.Vagrant:
		cloud = newVagrant(namespace)
		if cloud == nil {
			log.WithError(err).Error("Failed to create Vagrant cluster.")
		}
		cloudConfig = util.CloudConfigCoreOS(append(keys, VagrantPublicKey))
	default:
		panic("Unimplemented")
	}

	clst := cluster{
		provider:    cloud,
		id:          id,
		conn:        conn,
		cloudConfig: cloudConfig,
		trigger:     conn.TriggerTick(30, db.MachineTable),
		fm:          newForeman(conn, id),
	}

	go func() {
		defer clst.provider.disconnect()
		for range clst.trigger.C {
			clst.sync()
		}
	}()
	return &clst
}

func (clst cluster) sync() {
	/* Each iteration of this loop does the following:
	 *
	 * - Get the current set of machines from the cloud provider.
	 * - Get the current policy from the database.
	 * - Compute a diff.
	 * - Update the cloud provider accordingly.
	 *
	 * Updating the cloud provider may have consequences (creating machines for
	 * instances) that should be reflected in the database.  Therefore, if updates
	 * are necessary the code loops so that database can be updated before
	 * the next sync() call. */
	for i := 0; i < 8; i++ {
		cloudMachines, err := clst.get()
		if err != nil {
			log.WithError(err).Error("Failed to list machines.")
			return
		}

		nBoot, terminateSet := clst.syncDB(cloudMachines)
		if nBoot == 0 && len(terminateSet) == 0 {
			return
		}

		if nBoot > 0 {
			log.WithField("count", nBoot).Info("Attempt to boot machines.")
			err := clst.boot(nBoot, clst.cloudConfig)
			if err != nil {
				log.WithError(err).Warn("Failed to boot machines.")
			} else {
				log.Info("Successfully booted machines.")
			}
		}

		if len(terminateSet) > 0 {
			log.WithField("machines", terminateSet).Info("Attempt to stop.")
			if err := clst.stop(terminateSet); err != nil {
				log.WithError(err).Error("Failed to stop machines.")
			} else {
				log.Info("Successfully stopped machines.")
			}
		}
	}
}

func (clst cluster) syncDB(cloudMachines []machine) (int, []string) {
	var nBoot int
	var terminateSet []string
	clst.conn.Transact(func(view db.Database) error {
		machines := view.SelectFromMachine(func(m db.Machine) bool {
			return m.ClusterID == clst.id
		})

		cloudMap := make(map[string]machine)
		for _, m := range cloudMachines {
			cloudMap[m.id] = m
		}

		var unassigned []db.Machine
		for _, m := range machines {
			if cm, ok := cloudMap[m.CloudID]; ok {
				writeMachine(view, m, cm)
				delete(cloudMap, m.CloudID)
			} else {
				unassigned = append(unassigned, m)
			}
		}

		for id, m := range cloudMap {
			if len(unassigned) == 0 {
				terminateSet = append(terminateSet, id)
			} else {
				writeMachine(view, unassigned[0], m)
				unassigned = unassigned[1:]
			}
		}
		nBoot = len(unassigned)

		return nil
	})

	return nBoot, terminateSet
}

func writeMachine(view db.Database, dbm db.Machine, m machine) {
	dbm.CloudID = m.id
	dbm.PublicIP = m.publicIP
	dbm.PrivateIP = m.privateIP
	view.Commit(dbm)
}
