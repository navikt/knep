package hostmap

import (
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"testing"
)

const (
	onpremFirewallYaml = `
oracle:
- host: db-scan.nav.no
  port: 1521
  ips:
  - "2.3.4.5"
  - "6.7.8.9"
  - "10.11.12.13"
  scan:
  - host: db1-vip.adeo.no
    ip: "14.15.16.17"
    port: 1521
  - host: db2-vip.adeo.no 
    ip: "18.19.20.21"
    port: 1521
  - host: db3-vip.adeo.no 
    ip: "22.23.24.25"
    port: 1521
  - host: db4-vip.adeo.no
    ip: "26.27.28.29"
    port: 1521
- host: db5.adeo.no
  ips: 
  - "44.55.66.77"
  port: 1521
`
)

func Test_CreatePortHostMap(t *testing.T) {
	firewallMapfile, err := ioutil.TempFile("/tmp", "onprem-firewall.yaml")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(firewallMapfile.Name())
	_, err = firewallMapfile.Write([]byte(onpremFirewallYaml))
	if err != nil {
		t.Fatal(err)
	}

	hostMap, err := New(firewallMapfile.Name())
	if err != nil {
		t.Fatal(err)
	}

	type args struct {
		hosts []string
	}
	tests := []struct {
		name string
		args args
		want AllowIPFQDN
	}{
		{
			name: "Test create host map",
			args: args{
				hosts: []string{
					"google.com",
					"db.nav.no:5432",
					"db2.nav.no:5432",
					"123.123.123.123:22",
					"1.1.1.1:8080",
				},
			},
			want: AllowIPFQDN{
				FQDN: map[int32][]string{
					443:  {"google.com"},
					5432: {"db.nav.no", "db2.nav.no"},
				},
				IP: map[int32][]string{
					22:   {"123.123.123.123"},
					8080: {"1.1.1.1"},
				},
			},
		},
		{
			name: "Test create host map with oracle scan hosts",
			args: args{
				hosts: []string{
					"google.com",
					"db-scan.nav.no:1521",
					"1.1.1.1:8080",
				},
			},
			want: AllowIPFQDN{
				FQDN: map[int32][]string{
					443:  {"google.com"},
					1521: {"db-scan.nav.no", "db1-vip.adeo.no", "db2-vip.adeo.no", "db3-vip.adeo.no", "db4-vip.adeo.no"},
				},
				IP: map[int32][]string{
					8080: {"1.1.1.1"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostmap, err := hostMap.CreatePortHostMap(tt.args.hosts)
			if err != nil {
				t.Error(err)
			}

			if !reflect.DeepEqual(hostmap, tt.want) {
				t.Errorf("parse() = %v, want %v", hostmap, tt.want)
			}
		})
	}
}
