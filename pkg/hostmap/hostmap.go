package hostmap

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type Host struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type OracleHost struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Scan []Host `json:"scan"`
}

type Hosts struct {
	Oracle []OracleHost `json:"oracle"`
}

type AllowIPFQDN struct {
	IP   map[int32][]string
	FQDN map[int32][]string
}

type HostMap struct {
	oracleScanHosts map[string]OracleHost
}

func New(onpremFirewallPath string) (*HostMap, error) {
	oracleScanHosts, err := getOracleScanHosts(onpremFirewallPath)
	if err != nil {
		return nil, err
	}

	return &HostMap{
		oracleScanHosts: oracleScanHosts,
	}, nil
}

func getOracleScanHosts(onpremFirewallPath string) (map[string]OracleHost, error) {
	dataBytes, err := os.ReadFile(onpremFirewallPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", onpremFirewallPath, err)
	}

	var hostMap Hosts
	if err := yaml.Unmarshal(dataBytes, &hostMap); err != nil {
		return nil, err
	}

	oracleScanHosts := map[string]OracleHost{}
	for _, oracleHost := range hostMap.Oracle {
		if len(oracleHost.Scan) > 0 {
			oracleScanHosts[oracleHost.Host] = oracleHost
		}
	}

	return oracleScanHosts, nil
}

func (h *HostMap) CreatePortHostMap(hosts []string) (AllowIPFQDN, error) {
	ipRegex := regexp.MustCompile(`((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}`)
	allow := AllowIPFQDN{
		IP:   make(map[int32][]string),
		FQDN: make(map[int32][]string),
	}

	for _, hostPort := range hosts {
		parts := strings.Split(hostPort, ":")
		host := parts[0]
		portInt := int32(443)
		if len(parts) > 1 {
			port := parts[1]
			tmp, err := strconv.Atoi(port)
			if err != nil {
				return AllowIPFQDN{}, err
			}
			portInt = int32(tmp)
		}

		if ipRegex.MatchString(host) {
			allow.IP[portInt] = append(allow.IP[portInt], host)
		} else {
			allow.FQDN[portInt] = append(allow.FQDN[portInt], host)

			if scanHosts, ok := h.oracleScanHosts[host]; ok {
				for _, scanHost := range scanHosts.Scan {
					allow.FQDN[portInt] = append(allow.FQDN[portInt], scanHost.Host)
				}
			}
		}
	}

	return allow, nil
}
