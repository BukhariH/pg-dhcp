// This source file is part of the PG-DHCP project.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dhcp

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/usi-lfkeitel/packet-guardian/src/common"
)

func TestIPGiveOut(t *testing.T) {
	// Setup environment
	e := common.NewTestEnvironment()

	// Setup Confuration
	reader := strings.NewReader(testConfig)
	c, err := newParser(bufio.NewScanner(reader)).parse()
	if err != nil {
		t.Fatalf("Test config failed parsing: %v", err)
	}

	pool := c.networks["network1"].subnets[0].pools[0]
	lease := pool.getFreeLease(e)
	if !bytes.Equal(lease.IP.To4(), []byte{0xa, 0x0, 0x1, 0xa}) {
		t.Errorf("Incorrect lease. Expected %v, got %v", []byte{0xa, 0x0, 0x2, 0xa}, lease.IP)
	}
	lease.End = time.Now().Add(time.Duration(10) * time.Second)

	// Test next lease is given
	lease = pool.getFreeLease(e)
	if !bytes.Equal(lease.IP.To4(), []byte{0xa, 0x0, 0x1, 0xb}) {
		t.Errorf("Incorrect lease. Expected %v, got %v", []byte{0xa, 0x0, 0x2, 0xb}, lease.IP)
	}
}

func BenchmarkLeaseGiveOutLastLeaseNet24(b *testing.B) {
	benchmarkPool("Network1", b)
}

func BenchmarkLeaseGiveOutLastLeaseNet22(b *testing.B) {
	benchmarkPool("Network2", b)
}

func benchmarkPool(name string, b *testing.B) {
	// Setup environment
	e := common.NewTestEnvironment()

	// Setup Confuration
	reader := strings.NewReader(testConfig)
	c, err := newParser(bufio.NewScanner(reader)).parse()
	if err != nil {
		b.Fatalf("Test config failed parsing: %v", err)
	}

	pool := c.networks[name].subnets[0].pools[0]
	// Burn through all but the last lease
	for i := 0; i < pool.getCountOfIPs()-1; i++ {
		lease := pool.getFreeLease(e)
		if lease == nil {
			b.FailNow()
		}
		lease.End = time.Now().Add(time.Duration(100) * time.Second)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lease := pool.getFreeLease(e)
		if lease == nil {
			b.Fatal("Lease is nil")
		}
	}
}
