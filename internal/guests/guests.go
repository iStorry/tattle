// Package guests finds the VMs and containers defined on a Proxmox host by
// reading their config files, so traffic can be shown by name instead of by
// MAC address. It needs no API token: /etc/pve is readable by root.
package guests

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/model"
)

// Load returns every VM and container found under pveDir (normally /etc/pve).
// In a cluster it includes guests on other nodes, so they are still shown by
// name when they talk to a guest on this one.
func Load(pveDir string) ([]model.Machine, error) {
	var out []model.Machine
	seen := map[string]bool{}
	add := func(pattern, kind, node string) {
		files, _ := filepath.Glob(pattern)
		for _, f := range files {
			m, ok := parseFile(f, kind)
			if !ok || seen[m.ID] {
				continue
			}
			if node == "" {
				node = filepath.Base(filepath.Dir(filepath.Dir(f)))
			}
			m.Node = node
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	nodes, _ := filepath.Glob(filepath.Join(pveDir, "nodes", "*"))
	for _, n := range nodes {
		add(filepath.Join(n, "qemu-server", "*.conf"), "vm", filepath.Base(n))
		add(filepath.Join(n, "lxc", "*.conf"), "ct", filepath.Base(n))
	}
	if len(nodes) == 0 {
		if _, err := os.Stat(pveDir); err != nil {
			return nil, fmt.Errorf("reading Proxmox config: %w", err)
		}
		add(filepath.Join(pveDir, "qemu-server", "*.conf"), "vm", "local")
		add(filepath.Join(pveDir, "lxc", "*.conf"), "ct", "local")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VMID < out[j].VMID })
	return out, nil
}

func parseFile(path, kind string) (model.Machine, bool) {
	vmid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(path), ".conf"))
	if err != nil {
		return model.Machine{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return model.Machine{}, false
	}
	defer f.Close()

	m := model.Machine{ID: fmt.Sprintf("%s-%d", kind, vmid), Kind: kind, VMID: vmid, MACs: []string{}, IPs: []string{}, Bridges: []string{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			break // snapshot sections follow; only the current config matters
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case key == "name" || key == "hostname":
			m.Name = val
		case key == "template" && val == "1":
			return model.Machine{}, false
		case isNetKey(key):
			mac, bridge, ip := parseNet(val)
			if mac != "" {
				m.MACs = append(m.MACs, mac)
			}
			if bridge != "" && !slices.Contains(m.Bridges, bridge) {
				m.Bridges = append(m.Bridges, bridge)
			}
			m.IPs = append(m.IPs, ip...)
		}
	}
	if m.Name == "" {
		m.Name = fmt.Sprintf("%s %d", strings.ToUpper(kind), vmid)
	}
	return m, true
}

func isNetKey(key string) bool {
	rest, ok := strings.CutPrefix(key, "net")
	if !ok || rest == "" {
		return false
	}
	_, err := strconv.Atoi(rest)
	return err == nil
}

// parseNet handles both forms:
//
//	VM:        virtio=BC:24:11:2E:5A:01,bridge=vmbr0,firewall=1
//	container: name=eth0,bridge=vmbr0,hwaddr=BC:24:11:AA:BB:CC,ip=192.168.1.5/24
func parseNet(val string) (mac, bridge string, ips []string) {
	for _, part := range strings.Split(val, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "ip", "ip6":
			addr, _, _ := strings.Cut(v, "/")
			if strings.ContainsAny(addr, ".:") { // skips "dhcp", "auto", "manual"
				ips = append(ips, addr)
			}
		case "bridge":
			bridge = v
		case "name", "gw", "gw6", "tag", "rate", "mtu", "queues", "firewall", "link_down", "trunks", "type":
		default:
			// The key is "hwaddr", "macaddr" or the NIC model (virtio, e1000, ...).
			if m, ok := decode.ParseMAC(v); ok {
				mac = m.String()
			}
		}
	}
	return mac, bridge, ips
}
