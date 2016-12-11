package command

import (
	"github.com/NetSys/quilt/api/server"
	"github.com/NetSys/quilt/cluster"
	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/engine"
)

// Daemon contains the options for running the Quilt daemon.
type Daemon struct {
	*commonFlags
}

// NewDaemonCommand creates a new Daemon command instance.
func NewDaemonCommand() *Daemon {
	return &Daemon{
		commonFlags: &commonFlags{},
	}
}

// Parse parses the command line arguments for the daemon command.
func (dCmd *Daemon) Parse(args []string) error {
	return nil
}

// Run starts the daemon.
func (dCmd *Daemon) Run() int {
	conn := db.New()
	go engine.Run(conn)
	go server.Run(conn, dCmd.host)
	cluster.Run(conn)
	return 0
}
