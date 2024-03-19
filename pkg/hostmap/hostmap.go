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
	Port string   `json:"port"`
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
		portInts := []int32{443}
		var err error
		if len(parts) > 1 {
			portInts, err = getPorts(parts[1])
			if err != nil {
				return AllowIPFQDN{}, err
			}
		}

		if ipRegex.MatchString(host) {
			allow.IP = appendPortsHost(allow.IP, portInts, []string{host})
		} else {
			if hostConfig, ok := h.onpremHosts[host]; ok {
				allow.IP = appendPortsHost(allow.IP, portInts, hostConfig.IPs)
				for _, scanHost := range hostConfig.Scan {
					if scanHostConfig, ok := h.onpremHosts[scanHost]; ok {
						allow.IP = appendPortsHost(allow.IP, portInts, scanHostConfig.IPs)
					} else {
						allow.FQDN = appendPortsFQDNHost(allow.FQDN, portInts, scanHost)
					}
				}
			} else {
				allow.FQDN = appendPortsFQDNHost(allow.FQDN, portInts, host)
			}
		}
	}

	return allow, nil
}

func getPorts(ports string) ([]int32, error) {
	if portParts := strings.Split(ports, "-"); len(portParts) == 2 {
		startPort, err := strconv.Atoi(portParts[0])
		if err != nil {
			return []int32{}, err
		}
		endPort, err := strconv.Atoi(portParts[1])
		if err != nil {
			return []int32{}, err
		}

		portInts := []int32{}
		for port := startPort; port <= endPort; port++ {
			portInts = append(portInts, int32(port))
		}

		return portInts, nil
	}

	tmp, err := strconv.Atoi(ports)
	if err != nil {
		return []int32{}, err
	}

	return []int32{int32(tmp)}, nil
}

func appendPortsFQDNHost(allow map[int32][]string, portInts []int32, host string) map[int32][]string {
	if !isValidHostName(host) {
		return allow
	}

	return appendPortsHost(allow, portInts, []string{host})
}

func isValidHostName(host string) bool {
	r, _ := regexp.Compile(`^\w[\w\-\.]*\.[\w\-\.]*\w$`)
	return r.MatchString(host)
}

func appendPortsHost(allow map[int32][]string, portInts []int32, host []string) map[int32][]string {
	for _, portInt := range portInts {
		allow[portInt] = append(allow[portInt], host...)
	}

	return allow
}
