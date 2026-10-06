package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWrite(t *testing.T) {
	devs := []Device{
		{Target: "down:161"},
		{
			Target: "r1:161", SysName: `edge "01"`, Up: true, UptimeS: 42,
			Names:  map[string]string{"1": "Gi0/0/0"},
			Values: map[string]map[string]float64{"snmp_if_in_octets_total": {"1": 1000}},
		},
	}
	var buf bytes.Buffer
	write(&buf, devs)
	out := buf.String()

	for _, want := range []string{
		`snmp_up{target="down:161"} 0`,
		`snmp_up{target="r1:161"} 1`,
		`snmp_sys_uptime_seconds{target="r1:161",sysname="edge \"01\""} 42`,
		`snmp_if_in_octets_total{target="r1:161",ifindex="1",ifname="Gi0/0/0"} 1000`,
		"# TYPE snmp_if_in_octets_total counter",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, `snmp_sys_uptime_seconds{target="down:161"`) {
		t.Error("down target must not report uptime")
	}
}
