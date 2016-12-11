package ip

import (
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskToInt(t *testing.T) {
	mask := net.CIDRMask(16, 32)
	assert.Equal(t, uint32(0xffff0000), MaskToInt(mask))

	mask = net.CIDRMask(19, 32)
	assert.Equal(t, uint32(0xffffe000), MaskToInt(mask))

	mask = net.CIDRMask(32, 32)
	assert.Equal(t, uint32(0xffffffff), MaskToInt(mask))
}

func TestAllocate(t *testing.T) {
	prefix := net.IPv4(0xab, 0xcd, 0xe0, 0x00)
	mask := net.CIDRMask(20, 32)
	pool := NewPool(prefix, mask)
	conflicts := map[string]struct{}{}

	// Only 4k IPs, in 0xfffff000. Guaranteed a collision
	for i := 0; i < 5000; i++ {
		ip, err := pool.Allocate()
		if err != nil {
			continue
		}

		if _, ok := conflicts[ip.String()]; ok {
			t.Fatalf("IP Double allocation: 0x%x", ip)
		}

		assert.Equal(t, prefix.Mask(mask), ip.Mask(mask))
		conflicts[ip.String()] = struct{}{}
	}

	assert.Equal(t, len(conflicts), len(pool.ipSet))
	if len(conflicts) < 2500 || len(conflicts) > 4096 {
		// If the code's working, this is possible but *extremely* unlikely.
		// Probably a bug.
		t.Errorf("Too few conflicts: %d", len(conflicts))
	}
}

func TestAddIP(t *testing.T) {
	// Test that added IPs are not allocated.
	for i := 0; i < 10; i++ {
		testAddIP(t)
	}

	prefix := net.IPv4(10, 0, 0, 0)
	mask := net.CIDRMask(20, 32)
	pool := NewPool(prefix, mask)

	// Test that AddIP errors when the IP is out of the subnet.
	for i := 0; i < 256; i++ {
		a, b, c, d := 11+rand.Intn(200), rand.Intn(200),
			rand.Intn(200), rand.Intn(200)
		addr := net.IPv4(byte(a), byte(b), byte(c), byte(d))
		err := pool.AddIP(addr.String())
		assert.NotNil(t, err)
	}
}

func testAddIP(t *testing.T) {
	prefix := net.IPv4(10, 0, 0, 0)
	mask := net.CIDRMask(28, 32)
	pool := NewPool(prefix, mask)

	ipSet := map[string]struct{}{}
	for i := 0; i < 16; i++ {
		ipSet["10.0.0."+strconv.Itoa(i)] = struct{}{}
	}

	for i := 0; i < 4; i++ {
		j := ""
		for {
			j = "10.0.0." + strconv.Itoa(rand.Intn(16))
			if _, ok := ipSet[j]; ok {
				break
			}
		}

		pool.AddIP(j)
		delete(ipSet, j)
	}

	allocSet := map[string]struct{}{}
	for i := 0; i < 12; i++ {
		addr, err := pool.Allocate()
		assert.Nil(t, err)
		allocSet[addr.String()] = struct{}{}
	}

	assert.Equal(t, ipSet, allocSet)
}

func TestToMac(t *testing.T) {
	for i := 0; i < 256; i++ {
		a, b, c, d := rand.Intn(256), rand.Intn(256),
			rand.Intn(256), rand.Intn(256)
		addr := net.IPv4(byte(a), byte(b), byte(c), byte(d))
		exp := fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", a, b, c, d)
		assert.Equal(t, exp, ToMac(addr.String()))
	}
}

func sliceToSet(slice []string) map[string]struct{} {
	res := map[string]struct{}{}
	for _, s := range slice {
		res[s] = struct{}{}
	}
	return res
}
