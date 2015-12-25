//go:generate protoc ./pb/pb.proto --go_out=plugins=grpc:.
package minion

import (
	"github.com/NetSys/di/minion/supervisor"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("main")

func main() {
	log.Info("Minion Start")

	mServer := NewMinionServer()
	sv := supervisor.New(mServer.ContainerChan)
	for cfg := range mServer.ConfigChan {
		log.Info("Received Configuration: %s", cfg)
		sv.Configure(cfg)
	}
}
