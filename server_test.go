// This source file is part of the Packet Guardian project.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dhcp

import (
	"bufio"
	"bytes"
	"database/sql/driver"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	d4 "github.com/onesimus-systems/dhcp4"
	"github.com/usi-lfkeitel/packet-guardian/src/common"
)

var testConfig = `
global
	option domain-name example.com

	server-identifier 10.0.0.1

	registered
		default-lease-time 86400
		max-lease-time 86400
		option domain-name-server 10.1.0.1, 10.1.0.2
	end

	unregistered
		default-lease-time 360
		max-lease-time 360
		option domain-name-server 10.0.0.1
	end
end

network Network1
	unregistered
		subnet 10.0.1.0/24
			range 10.0.1.10 10.0.1.200
			option router 10.0.1.1
		end
	end
	registered
		subnet 10.0.2.0/24
			range 10.0.2.10 10.0.2.200
			option router 10.0.2.1
		end
	end
end

network Network2
	unregistered
		subnet 10.0.4.0/22
			range 10.0.4.1 10.0.7.254
			option router 10.0.4.1
		end
	end
	registered
		subnet 10.0.3.0/24
			range 10.0.3.10 10.0.3.200
			option router 10.0.3.1
		end
	end
end
`

var m sqlmock.Sqlmock

type timeChecker struct{}

func (t timeChecker) Match(v driver.Value) bool {
	return (v.(int64) > 0)
}

func setUpTest1(t *testing.T) *Handler {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}

	rows := sqlmock.NewRows(common.DeviceTableRows).AddRow(
		1, "12:34:56:12:34:56", "testUser", "192.168.1.1", "", time.Now().Add(time.Duration(15)*time.Second).Unix(), 0, "", false, "", 0,
	)

	rows2 := sqlmock.NewRows(common.DeviceTableRows).AddRow(
		1, "12:34:56:12:34:56", "testUser", "192.168.1.1", "", time.Now().Add(time.Duration(15)*time.Second).Unix(), 0, "", false, "", 0,
	)

	mock.ExpectQuery("SELECT .*? FROM \"device\"").
		WithArgs("12:34:56:12:34:56").
		WillReturnRows(rows)

	mock.ExpectQuery("SELECT .*? FROM \"device\"").
		WithArgs("12:34:56:12:34:56").
		WillReturnRows(rows2)

	mock.ExpectExec("INSERT INTO \"lease\"").
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectExec("UPDATE \"device\"").
		WithArgs(
			"12:34:56:12:34:56", "testUser", "192.168.1.1", "", sqlmock.AnyArg(), 0, "", false, "", timeChecker{}, 1,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectQuery("SELECT .*? FROM \"device\"").
		WithArgs("12:34:56:12:34:56").
		WillReturnRows(sqlmock.NewRows(common.DeviceTableRows))

	mock.ExpectQuery("SELECT .*? FROM \"device\"").
		WithArgs("12:34:56:12:34:56").
		WillReturnRows(sqlmock.NewRows(common.DeviceTableRows))

	mock.ExpectExec("INSERT INTO \"lease\"").
		WillReturnResult(sqlmock.NewResult(2, 1))

	// Setup environment
	e := common.NewTestEnvironment()
	e.DB = &common.DatabaseAccessor{DB: db}

	// Setup Confuration
	reader := strings.NewReader(testConfig)
	c, err := newParser(bufio.NewScanner(reader)).parse()
	if err != nil {
		t.Fatalf("Test config failed parsing: %v", err)
	}

	m = mock
	return NewDHCPServer(c, e)
}

func TestDiscover(t *testing.T) {
	server := setUpTest1(t)
	defer server.e.DB.DB.Close()

	// Create test request packet
	mac, _ := net.ParseMAC("12:34:56:12:34:56")
	opts := []d4.Option{
		d4.Option{
			Code:  d4.OptionParameterRequestList,
			Value: []byte{0x1, 0x3, 0x6, 0xf, 0x23},
		},
	}
	p := d4.RequestPacket(d4.Discover, mac, nil, nil, false, opts)
	p.SetGIAddr(net.ParseIP("10.0.1.5"))

	// Process a DISCOVER request
	start := time.Now()
	dp := server.ServeDHCP(p, d4.Discover, p.ParseOptions())
	t.Logf("Discover took: %v", time.Since(start))

	if dp == nil {
		t.Fatal("Processed packet is nil")
	}

	checkIP(dp, []byte{0xa, 0x0, 0x2, 0xa}, t)
	options := checkOptions(dp, d4.Options{
		d4.OptionSubnetMask:         []byte{0xff, 0xff, 0xff, 0x0},
		d4.OptionRouter:             []byte{0xa, 0x0, 0x2, 0x1},
		d4.OptionDomainNameServer:   []byte{0xa, 0x1, 0x0, 0x1, 0xa, 0x1, 0x0, 0x2},
		d4.OptionDomainName:         []byte("example.com"),
		d4.OptionIPAddressLeaseTime: []byte{0x0, 0x1, 0x51, 0x80},
	}, t)

	opts = []d4.Option{
		d4.Option{
			Code:  d4.OptionParameterRequestList,
			Value: []byte{0x1, 0x3, 0x6, 0xf, 0x23},
		},
		d4.Option{
			Code:  d4.OptionServerIdentifier,
			Value: []byte(options[d4.OptionServerIdentifier]),
		},
		d4.Option{
			Code:  d4.OptionRequestedIPAddress,
			Value: []byte(dp.YIAddr().To4()),
		},
	}
	p = d4.RequestPacket(d4.Request, mac, nil, nil, false, opts)
	p.SetGIAddr(net.ParseIP("10.0.1.5"))

	// Process a REQUEST request
	start = time.Now()
	rp := server.ServeDHCP(p, d4.Request, p.ParseOptions())
	t.Logf("Request took: %v", time.Since(start))

	if rp == nil {
		t.Fatal("Processed packet is nil")
	}

	checkIP(rp, dp.YIAddr(), t)
	checkOptions(rp, d4.Options{
		d4.OptionDHCPMessageType:    []byte{0x5},
		d4.OptionSubnetMask:         []byte{0xff, 0xff, 0xff, 0x0},
		d4.OptionRouter:             []byte{0xa, 0x0, 0x2, 0x1},
		d4.OptionDomainNameServer:   []byte{0xa, 0x1, 0x0, 0x1, 0xa, 0x1, 0x0, 0x2},
		d4.OptionDomainName:         []byte("example.com"),
		d4.OptionIPAddressLeaseTime: []byte{0x0, 0x1, 0x51, 0x80},
	}, t)

	// ROUND 2 - Fight!
	opts = []d4.Option{
		d4.Option{
			Code:  d4.OptionParameterRequestList,
			Value: []byte{0x1, 0x3, 0x6, 0xf, 0x23},
		},
	}
	p = d4.RequestPacket(d4.Discover, mac, nil, nil, false, opts)
	p.SetGIAddr(net.ParseIP("10.0.1.5"))

	// Process a DISCOVER request
	start = time.Now()
	dp = server.ServeDHCP(p, d4.Discover, p.ParseOptions())
	t.Logf("Discover took: %v", time.Since(start))

	if dp == nil {
		t.Fatal("Processed packet is nil")
	}

	checkIP(dp, []byte{0xa, 0x0, 0x1, 0xa}, t)
	checkOptions(dp, d4.Options{
		d4.OptionSubnetMask:         []byte{0xff, 0xff, 0xff, 0x0},
		d4.OptionRouter:             []byte{0xa, 0x0, 0x1, 0x1},
		d4.OptionDomainNameServer:   []byte{0xa, 0x0, 0x0, 0x1},
		d4.OptionDomainName:         []byte("example.com"),
		d4.OptionIPAddressLeaseTime: []byte{0x0, 0x0, 0x1, 0x68},
	}, t)

	opts = []d4.Option{
		d4.Option{
			Code:  d4.OptionParameterRequestList,
			Value: []byte{0x1, 0x3, 0x6, 0xf, 0x23},
		},
		d4.Option{
			Code:  d4.OptionServerIdentifier,
			Value: []byte(options[d4.OptionServerIdentifier]),
		},
		d4.Option{
			Code:  d4.OptionRequestedIPAddress,
			Value: []byte(dp.YIAddr().To4()),
		},
	}
	p = d4.RequestPacket(d4.Request, mac, nil, nil, false, opts)
	p.SetGIAddr(net.ParseIP("10.0.1.5"))

	// Process a REQUEST request
	start = time.Now()
	rp = server.ServeDHCP(p, d4.Request, p.ParseOptions())
	t.Logf("Request took: %v", time.Since(start))

	if rp == nil {
		t.Fatal("Processed packet is nil")
	}

	checkIP(rp, dp.YIAddr(), t)
	checkOptions(rp, d4.Options{
		d4.OptionDHCPMessageType:    []byte{0x5},
		d4.OptionSubnetMask:         []byte{0xff, 0xff, 0xff, 0x0},
		d4.OptionRouter:             []byte{0xa, 0x0, 0x1, 0x1},
		d4.OptionDomainNameServer:   []byte{0xa, 0x0, 0x0, 0x1},
		d4.OptionDomainName:         []byte("example.com"),
		d4.OptionIPAddressLeaseTime: []byte{0x0, 0x0, 0x1, 0x68},
	}, t)

	// we make sure that all expectations were met
	if err := m.ExpectationsWereMet(); err != nil {
		t.Errorf("There were unfulfilled expections: %s", err)
	}
}

func checkIP(p d4.Packet, expected net.IP, t *testing.T) {
	if !bytes.Equal(p.YIAddr().To4(), expected.To4()) {
		t.Errorf("Incorrect IP. Expected %v, got %v", expected, p.YIAddr())
	}
}

func checkOptions(p d4.Packet, ops d4.Options, t *testing.T) d4.Options {
	options := p.ParseOptions()
	for o, v := range ops {
		if val, ok := options[o]; !ok { // 0x23 (51)
			t.Errorf("%s not received", o.String())
		} else if !bytes.Equal(val, v) {
			t.Errorf("Incorrect %s. Expected %v, got %v", o.String(), v, val)
		}
	}
	return options
}

func BenchmarkDHCPDiscover(b *testing.B) {
	// Set up mock database
	db, mock, err := sqlmock.New()
	if err != nil {
		b.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}

	// Setup environment
	e := common.NewTestEnvironment()
	e.DB = &common.DatabaseAccessor{DB: db}

	// Setup Confuration
	reader := strings.NewReader(testConfig)
	c, err := newParser(bufio.NewScanner(reader)).parse()
	if err != nil {
		b.Fatalf("Test config failed parsing: %v", err)
	}
	server := NewDHCPServer(c, e)
	pool := c.networks["Network1"].subnets[1].pools[0] // Registered pool

	// Create test request packet
	mac, _ := net.ParseMAC("12:34:56:12:34:56")
	opts := []d4.Option{
		d4.Option{
			Code:  d4.OptionParameterRequestList,
			Value: []byte{0x1, 0x3, 0x6, 0xf, 0x23},
		},
	}
	p := d4.RequestPacket(d4.Discover, mac, nil, nil, false, opts)
	p.SetGIAddr(net.ParseIP("10.0.1.5"))
	unixZero := time.Unix(0, 0)
	expTime := time.Now().Add(time.Duration(100) * time.Second).Unix()

	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		rows := sqlmock.NewRows(common.DeviceTableRows).AddRow(
			1, "12:34:56:12:34:56", "testUser", "192.168.1.1", "", expTime, 0, "", false, "", 0,
		)
		mock.ExpectQuery("SELECT .*? FROM \"device\"").
			WithArgs("12:34:56:12:34:56").
			WillReturnRows(rows)

		b.StartTimer()
		dp := server.ServeDHCP(p, d4.Discover, p.ParseOptions())
		b.StopTimer()

		if dp == nil {
			b.Fatal("ServeDHCP returned nil")
		}
		pool.leases["10.0.2.10"].End = unixZero
	}
}
