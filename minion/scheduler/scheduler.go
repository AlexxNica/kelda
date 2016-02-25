package scheduler

import (
	"fmt"
	"reflect"
	"time"

	"github.com/NetSys/di/db"
	"github.com/NetSys/di/join"
	"github.com/NetSys/di/minion/docker"

	log "github.com/Sirupsen/logrus"
)

type scheduler interface {
	list() ([]docker.Container, error)

	boot(toBoot []db.Container)

	terminate(ids []string)
}

func Run(conn db.Conn) {
	var sched scheduler
	for range conn.TriggerTick(30, db.MinionTable, db.EtcdTable, db.ContainerTable).C {
		minions := conn.SelectFromMinion(nil)
		etcdRows := conn.SelectFromEtcd(nil)
		if len(minions) != 1 || len(etcdRows) != 1 || minions[0].Role != db.Master ||
			minions[0].PrivateIP == "" || !etcdRows[0].Leader {
			sched = nil
			continue
		}

		if sched == nil {
			ip := minions[0].PrivateIP
			sched = newSwarm(docker.New(fmt.Sprintf("tcp://%s:2377", ip)))
			time.Sleep(60 * time.Second)
		}

		// Each time we run through this loop, we may boot or terminate
		// containers.  These modification should, in turn, be reflected in the
		// database themselves.  For this reason, we attempt to sync until no
		// database modifications happen (up to an arbitrary limit of three
		// tries).
		for i := 0; i < 3; i++ {
			dkc, err := sched.list()
			if err != nil {
				log.WithError(err).Warning("Failed to get containers.")
				break
			}

			var boot []db.Container
			var term []string
			conn.Transact(func(view db.Database) error {
				term, boot = syncDB(view, dkc)
				return nil
			})

			if len(term) == 0 && len(boot) == 0 {
				break
			}
			sched.terminate(term)
			sched.boot(boot)
		}
	}
}

func syncDB(view db.Database, dkcs_ []docker.Container) ([]string, []db.Container) {
	score := func(left, right interface{}) int {
		dbc := left.(db.Container)
		dkc := right.(docker.Container)

		if dkc.Image != dbc.Image ||
			!reflect.DeepEqual(dkc.Command, dbc.Command) {
			return -1
		} else if dkc.ID == dbc.SchedID {
			return 0
		} else {
			return 1
		}
	}
	pairs, dbcs, dkcs := join.Join(view.SelectFromContainer(nil), dkcs_, score)

	for _, pair := range pairs {
		dbc := pair.L.(db.Container)
		dbc.SchedID = pair.R.(docker.Container).ID
		view.Commit(dbc)
	}

	var term []string
	for _, dkc := range dkcs {
		term = append(term, dkc.(docker.Container).ID)
	}

	var boot []db.Container
	for _, dbc := range dbcs {
		boot = append(boot, dbc.(db.Container))
	}

	return term, boot
}
