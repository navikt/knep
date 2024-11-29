package hostmap

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	onpremHostYaml = `
db.nav.no:
  port: 1521
  ips:
    - "1.2.3.4"
db2.nav.no:
  port: 1521
  ips:
    - "11.22.33.44"
db-scan.nav.no:
  port: 1521
  ips:
    - "2.3.4.5"
    - "6.7.8.9"
    - "10.11.12.13"
    - "14.15.16.17"
    - "18.19.20.21"
    - "22.23.24.25"
    - "26.27.28.29"
db1-vip.adeo.no:
  ips: 
    - "14.15.16.17"
  port: 1521
db2-vip.adeo.no:
  ips: 
    - "18.19.20.21"
  port: 1521
db3-vip.adeo.no:
  ips: 
    - "22.23.24.25"
  port: 1521
db4-vip.adeo.no:
  ips: 
    - "26.27.28.29"
  port: 1521
db5.adeo.no:
  ips: 
    - "44.55.66.77"
  port: 1521
informatica.nav.no:
  ips: 
    - "123.123.123.123"
  port: 6005-6010
`
	externalHostYaml = `
pypi.org:
  port: 443
  ips:
    - "151.101.0.0/16"
`
)

func Test_CreatePortHostMap(t *testing.T) {
	onpremHostMapFile, err := os.CreateTemp("/tmp", "onprem-firewall.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(onpremHostMapFile.Name())

	_, err = onpremHostMapFile.Write([]byte(onpremHostYaml))
	if err != nil {
		t.Fatal(err)
	}

	externalHostMapFile, err := os.CreateTemp("/tmp", "external-hosts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(externalHostMapFile.Name())

	_, err = externalHostMapFile.Write([]byte(externalHostYaml))
	if err != nil {
		t.Fatal(err)
	}

	hostMap, err := New(onpremHostMapFile.Name(), externalHostMapFile.Name())
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
					443: {"google.com"},
				},
				IP: map[int32][]string{
					22:   {"123.123.123.123"},
					8080: {"1.1.1.1"},
					5432: {"1.2.3.4", "11.22.33.44"},
				},
			},
		},
		{
			name: "Test create host map ensure lower case hostname",
			args: args{
				hosts: []string{
					"Google.com",
					"123.123.123.123:22",
				},
			},
			want: AllowIPFQDN{
				FQDN: map[int32][]string{
					443: {"google.com"},
				},
				IP: map[int32][]string{
					22: {"123.123.123.123"},
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
					443: {"google.com"},
				},
				IP: map[int32][]string{
					8080: {"1.1.1.1"},
					1521: {"2.3.4.5", "6.7.8.9", "10.11.12.13", "14.15.16.17", "18.19.20.21", "22.23.24.25", "26.27.28.29"},
				},
			},
		},
		{
			name: "Test create hostmap port range",
			args: args{
				hosts: []string{
					"google.com",
					"informatica.nav.no:6005-6010",
				},
			},
			want: AllowIPFQDN{
				FQDN: map[int32][]string{
					443: {"google.com"},
				},
				IP: map[int32][]string{
					6005: {"123.123.123.123"},
					6006: {"123.123.123.123"},
					6007: {"123.123.123.123"},
					6008: {"123.123.123.123"},
					6009: {"123.123.123.123"},
					6010: {"123.123.123.123"},
				},
			},
		},
		{
			name: "Test create hostmap external host with cidr",
			args: args{
				hosts: []string{
					"pypi.org",
					"google.com:123",
				},
			},
			want: AllowIPFQDN{
				IP: map[int32][]string{
					443: {"151.101.0.0/16"},
				},
				FQDN: map[int32][]string{
					123: {"google.com"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hostMap.CreatePortHostMap(tt.args.hosts)
			if err != nil {
				t.Error(err)
			}

			if diff := cmp.Diff(got, tt.want); diff != "" {
				t.Errorf("CreatePortHostMap() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
