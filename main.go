// snmp-exporter-go polls network devices over SNMP and exposes interface
// metrics in the Prometheus text format at /metrics.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// OIDs from SNMPv2-MIB, IF-MIB and IF-MIB ifXTable (64-bit counters).
const (
	oidSysUpTime    = ".1.3.6.1.2.1.1.3.0"
	oidSysName      = ".1.3.6.1.2.1.1.5.0"
	oidIfName       = ".1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCInOctets = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOcts  = ".1.3.6.1.2.1.31.1.1.1.10"
	oidIfOperStatus = ".1.3.6.1.2.1.2.2.1.8"
	oidIfInErrors   = ".1.3.6.1.2.1.2.2.1.14"
	oidIfOutErrors  = ".1.3.6.1.2.1.2.2.1.20"
)

// ifMetric maps a per-interface column to the metric it becomes.
type ifMetric struct {
	oid, name, help, kind string
}

var ifMetrics = []ifMetric{
	{oidIfHCInOctets, "snmp_if_in_octets_total", "Bytes received on the interface (ifHCInOctets).", "counter"},
	{oidIfHCOutOcts, "snmp_if_out_octets_total", "Bytes sent on the interface (ifHCOutOctets).", "counter"},
	{oidIfInErrors, "snmp_if_in_errors_total", "Inbound packets with errors (ifInErrors).", "counter"},
	{oidIfOutErrors, "snmp_if_out_errors_total", "Outbound packets with errors (ifOutErrors).", "counter"},
	{oidIfOperStatus, "snmp_if_oper_status", "Operational status: 1=up, 2=down, other values per IF-MIB.", "gauge"},
}

// Device is the result of polling one target.
type Device struct {
	Target   string
	SysName  string
	Up       bool
	UptimeS  float64
	Duration float64
	// Values[metric name][ifIndex] = value; Names[ifIndex] = ifName.
	Values map[string]map[string]float64
	Names  map[string]string
}

// poll queries one device. It never returns an error: a failed device is
// reported as snmp_up 0 so Prometheus can alert on it.
func poll(target, community string, timeout time.Duration) (d Device) {
	start := time.Now()
	d = Device{Target: target, Values: map[string]map[string]float64{}, Names: map[string]string{}}
	defer func() { d.Duration = time.Since(start).Seconds() }()

	host, port := target, uint16(161)
	if h, p, err := net.SplitHostPort(target); err == nil {
		n, _ := strconv.Atoi(p)
		host, port = h, uint16(n)
	}
	g := &gosnmp.GoSNMP{
		Target: host, Port: port, Community: community,
		Version: gosnmp.Version2c, Timeout: timeout, Retries: 1,
		MaxRepetitions: 25,
	}
	if err := g.Connect(); err != nil {
		log.Printf("%s: connect: %v", target, err)
		return d
	}
	defer g.Conn.Close()

	res, err := g.Get([]string{oidSysUpTime, oidSysName})
	if err != nil {
		log.Printf("%s: get system: %v", target, err)
		return d
	}
	for _, v := range res.Variables {
		switch v.Name {
		case oidSysUpTime:
			d.UptimeS = float64(gosnmp.ToBigInt(v.Value).Uint64()) / 100 // TimeTicks are 1/100 s
		case oidSysName:
			if b, ok := v.Value.([]byte); ok {
				d.SysName = string(b)
			}
		}
	}

	walk := func(oid string, fn func(idx string, v gosnmp.SnmpPDU)) error {
		return g.BulkWalk(oid, func(v gosnmp.SnmpPDU) error {
			fn(strings.TrimPrefix(v.Name, oid+"."), v)
			return nil
		})
	}
	if err := walk(oidIfName, func(idx string, v gosnmp.SnmpPDU) {
		if b, ok := v.Value.([]byte); ok {
			d.Names[idx] = string(b)
		}
	}); err != nil {
		log.Printf("%s: walk ifName: %v", target, err)
		return d
	}
	for _, m := range ifMetrics {
		vals := map[string]float64{}
		if err := walk(m.oid, func(idx string, v gosnmp.SnmpPDU) {
			f, _ := gosnmp.ToBigInt(v.Value).Float64()
			vals[idx] = f
		}); err != nil {
			log.Printf("%s: walk %s: %v", target, m.name, err)
			return d
		}
		d.Values[m.name] = vals
	}
	d.Up = true
	return d
}

// q quotes a string as a Prometheus label value.
func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// write renders devices in the Prometheus text exposition format.
func write(w io.Writer, devs []Device) {
	sort.Slice(devs, func(i, j int) bool { return devs[i].Target < devs[j].Target })

	header := func(name, help, kind string) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	}
	header("snmp_up", "1 if the last SNMP poll of the target succeeded.", "gauge")
	for _, d := range devs {
		fmt.Fprintf(w, "snmp_up{target=%s} %g\n", q(d.Target), b2f(d.Up))
	}
	header("snmp_scrape_duration_seconds", "Time spent polling the target.", "gauge")
	for _, d := range devs {
		fmt.Fprintf(w, "snmp_scrape_duration_seconds{target=%s} %g\n", q(d.Target), d.Duration)
	}
	header("snmp_sys_uptime_seconds", "Device uptime (sysUpTime).", "gauge")
	for _, d := range devs {
		if d.Up {
			fmt.Fprintf(w, "snmp_sys_uptime_seconds{target=%s,sysname=%s} %g\n", q(d.Target), q(d.SysName), d.UptimeS)
		}
	}
	for _, m := range ifMetrics {
		header(m.name, m.help, m.kind)
		for _, d := range devs {
			idxs := make([]string, 0, len(d.Values[m.name]))
			for idx := range d.Values[m.name] {
				idxs = append(idxs, idx)
			}
			sort.Strings(idxs)
			for _, idx := range idxs {
				fmt.Fprintf(w, "%s{target=%s,ifindex=%s,ifname=%s} %g\n",
					m.name, q(d.Target), q(idx), q(d.Names[idx]), d.Values[m.name][idx])
			}
		}
	}
}

func main() {
	listen := flag.String("listen", ":9161", "address to serve /metrics on")
	targets := flag.String("targets", "127.0.0.1:161", "comma-separated SNMP targets, host or host:port")
	community := flag.String("community", "public", "SNMP v2c community")
	timeout := flag.Duration("timeout", 3*time.Second, "SNMP timeout per request")
	flag.Parse()

	list := strings.Split(*targets, ",")
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		devs := make([]Device, len(list))
		var wg sync.WaitGroup
		for i, t := range list {
			wg.Add(1)
			go func(i int, t string) {
				defer wg.Done()
				devs[i] = poll(strings.TrimSpace(t), *community, *timeout)
			}(i, t)
		}
		wg.Wait()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		write(w, devs)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "snmp-exporter-go: metrics at /metrics")
	})
	log.Printf("listening on %s, polling %d target(s)", *listen, len(list))
	log.Fatal(http.ListenAndServe(*listen, nil))
}
