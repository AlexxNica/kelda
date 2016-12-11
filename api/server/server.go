package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NetSys/quilt/api"
	"github.com/NetSys/quilt/api/pb"
	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/minion/ip"
	"github.com/NetSys/quilt/stitch"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	log "github.com/Sirupsen/logrus"
)

type server struct {
	dbConn db.Conn
}

// Run accepts incoming `quiltctl` connections and responds to them.
func Run(conn db.Conn, listenAddr string) error {
	proto, addr, err := api.ParseListenAddress(listenAddr)
	if err != nil {
		return err
	}

	var sock net.Listener
	apiServer := server{conn}
	for {
		sock, err = net.Listen(proto, addr)

		if err == nil {
			break
		}
		log.WithError(err).Error("Failed to open socket.")

		time.Sleep(30 * time.Second)
	}

	// Cleanup the socket if we're interrupted.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGHUP)
	go func(c chan os.Signal) {
		sig := <-c
		log.Printf("Caught signal %s: shutting down.\n", sig)
		sock.Close()
		os.Exit(0)
	}(sigc)

	s := grpc.NewServer()
	pb.RegisterAPIServer(s, apiServer)
	s.Serve(sock)

	return nil
}

func (s server) Query(cts context.Context, query *pb.DBQuery) (*pb.QueryReply, error) {
	var rows interface{}
	err := s.dbConn.Transact(func(view db.Database) error {
		switch db.TableType(query.Table) {
		case db.MachineTable:
			rows = view.SelectFromMachine(nil)
		case db.ContainerTable:
			rows = view.SelectFromContainer(nil)
		case db.EtcdTable:
			rows = view.SelectFromEtcd(nil)
		case db.ConnectionTable:
			rows = view.SelectFromConnection(nil)
		case db.LabelTable:
			rows = view.SelectFromLabel(nil)
		case db.ClusterTable:
			rows = view.SelectFromCluster(nil)
		default:
			return fmt.Errorf("unrecognized table: %s", query.Table)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	json, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}

	return &pb.QueryReply{TableContents: string(json)}, nil
}

func (s server) Deploy(cts context.Context, deployReq *pb.DeployRequest) (
	*pb.DeployReply, error) {

	stitch, err := stitch.FromJSON(deployReq.Deployment)
	if err != nil {
		return &pb.DeployReply{}, err
	}

	if len(stitch.Machines) > ip.MaxMinionCount {
		return &pb.DeployReply{}, fmt.Errorf("cannot boot more than %d "+
			"machines", ip.MaxMinionCount)
	}

	err = s.dbConn.Transact(func(view db.Database) error {
		cluster, err := view.GetCluster()
		if err != nil {
			cluster = view.InsertCluster()
		}

		cluster.Spec = stitch.String()
		view.Commit(cluster)
		return nil
	})

	return &pb.DeployReply{}, err
}
