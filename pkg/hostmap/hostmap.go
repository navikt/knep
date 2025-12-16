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
}

type ExternalHost struct {
	IPs  []string `json:"ips"`
	Port string   `json:"port"`
}

type AllowIPFQDN struct {
	IP   map[int32][]string
	FQDN map[int32][]string
}

type HostMap struct {
	onpremHosts   map[string]OnpremHost
	externalHosts map[string]ExternalHost
}

func New(onpremHostMapFilePath, externalHostMapFilePath string) (*HostMap, error) {
	dataBytes, err := os.ReadFile(onpremHostMapFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", onpremHostMapFilePath, err)
	}

	var onpremHostMap map[string]OnpremHost
	if err := yaml.Unmarshal(dataBytes, &onpremHostMap); err != nil {
		return nil, err
	}

	dataBytes, err = os.ReadFile(externalHostMapFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", externalHostMapFilePath, err)
	}

	var externalHostMap map[string]ExternalHost
	if err := yaml.Unmarshal(dataBytes, &externalHostMap); err != nil {
		return nil, err
	}

	return &HostMap{
		onpremHosts:   onpremHostMap,
		externalHosts: externalHostMap,
	}, nil
}

func (h *HostMap) CreatePortHostMap(hosts []string) (AllowIPFQDN, error) {
	ipRegex := regexp.MustCompile(`((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}`)
	allow := AllowIPFQDN{
		IP:   make(map[int32][]string),
		FQDN: make(map[int32][]string),
	}

	for _, hostPort := range hosts {
		parts := strings.Split(trimScheme(hostPort), ":")
		host := strings.Split(parts[0], "/")[0] // Remove host path if present
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
			} else if hostConfig, ok := h.externalHosts[host]; ok {
				allow.IP = appendPortsHost(allow.IP, portInts, hostConfig.IPs)
			} else {
				allow.FQDN = appendPortsFQDNHost(allow.FQDN, portInts, host)
			}
		}
	}

	return allow, nil
}

func trimScheme(host string) string {
	parts := strings.Split(host, "//")
	if len(parts) == 2 {
		return parts[1]
	}

	return host
}

func getPorts(ports string) ([]int32, error) {
	//  Comma-separated ports
	if strings.Contains(ports, ",") {
		multiplePorts := []int32{}

		for _, part := range strings.Split(ports, ",") {
			addPort, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return []int32{}, err
			}
			multiplePorts = append(multiplePorts, int32(addPort))
		}

		return multiplePorts, nil
	}

	// Port ranges and single ports
	re := regexp.MustCompile(`\b\d+(-\d+)?\b`)
	ports = re.FindString(ports)
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

	return appendPortsHost(allow, portInts, []string{strings.ToLower(host)})
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
