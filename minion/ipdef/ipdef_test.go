package ipdef

import (
	"fmt"
	"math/rand"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToMac(t *testing.T) {
	for i := 0; i < 256; i++ {
		a, b, c, d := rand.Intn(256), rand.Intn(256),
			rand.Intn(256), rand.Intn(256)
		addr := net.IPv4(byte(a), byte(b), byte(c), byte(d))
		exp := fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", a, b, c, d)
		assert.Equal(t, exp, ToMac(addr.String()))
	}
}
