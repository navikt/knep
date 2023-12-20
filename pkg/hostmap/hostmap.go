package hostmap

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

type OnpremHost struct {
	IPs  []string `json:"ips"`
	Port int      `json:"port"`
	Scan []string `json:"scan"`
}

type AllowIPFQDN struct {
	IP   map[int32][]string
	FQDN map[int32][]string
}

type HostMap struct {
	onpremHosts map[string]OnpremHost
}

func New(onpremFirewallPath string) (*HostMap, error) {
	onpremHosts, err := getOnpremHostMap(onpremFirewallPath)
	if err != nil {
		return nil, err
	}

	return &HostMap{
		onpremHosts: onpremHosts,
	}, nil
}

func getOnpremHostMap(onpremFirewallPath string) (map[string]OnpremHost, error) {
	dataBytes, err := os.ReadFile(onpremFirewallPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", onpremFirewallPath, err)
	}

	var onpremHostMap map[string]OnpremHost
	if err := yaml.Unmarshal(dataBytes, &onpremHostMap); err != nil {
		return nil, err
	}

	return onpremHostMap, nil
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
			if hostConfig, ok := h.onpremHosts[host]; ok {
				allow.IP[portInt] = append(allow.IP[portInt], hostConfig.IPs...)
				for _, scanHost := range hostConfig.Scan {
					if scanHostConfig, ok := h.onpremHosts[scanHost]; ok {
						allow.IP[portInt] = append(allow.IP[portInt], scanHostConfig.IPs...)
					} else {
						allow.FQDN[portInt] = append(allow.FQDN[portInt], scanHost)
					}
				}
			} else {
				allow.FQDN[portInt] = append(allow.FQDN[portInt], host)
			}
		}
	}

	return allow, nil
}
