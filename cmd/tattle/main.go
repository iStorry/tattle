// Tattle shows, in plain language, what every VM and container on a Proxmox
// host is talking to.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/istorry/tattle/internal/api"
	"github.com/istorry/tattle/internal/capture"
	"github.com/istorry/tattle/internal/decode"
	"github.com/istorry/tattle/internal/demo"
	"github.com/istorry/tattle/internal/guests"
	"github.com/istorry/tattle/internal/model"
	"github.com/istorry/tattle/internal/store"
	"github.com/istorry/tattle/internal/tracker"
	"github.com/istorry/tattle/internal/ui"
)

var version = "dev"

func main() {
	var (
		listen    = flag.String("listen", ":8477", "address for the dashboard")
		dbPath    = flag.String("db", "/var/lib/tattle/tattle.db", "where to keep history")
		ifaces    = flag.String("iface", "", "bridges to watch, comma separated (default: every vmbr* bridge)")
		pveDir    = flag.String("pve-dir", "/etc/pve", "Proxmox config directory")
		retention = flag.Int("retention", 7, "days of history to keep")
		localNets = flag.String("local-net", "", "extra networks to count as inside your network, comma separated (e.g. 10.20.30.0/24)")
		demoMode  = flag.Bool("demo", false, "show invented traffic instead of capturing (works on any computer)")
		showVer   = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("tattle", version)
		return
	}
	log.SetFlags(log.Ldate | log.Ltime)

	if err := run(*listen, *dbPath, *ifaces, *pveDir, *localNets, *retention, *demoMode); err != nil {
		log.Fatalf("tattle: %v", err)
	}
}

func run(listen, dbPath, ifaceFlag, pveDir, localNets string, retention int, demoMode bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	status := &api.Status{Version: version, Demo: demoMode, RetentionDays: retention, Started: time.Now(), Interfaces: []string{}}
	errc := make(chan error, 8)

	extra, err := parsePrefixes(localNets)
	if err != nil {
		return err
	}
	if demoMode {
		status.LocalNets = append(extra, netip.MustParsePrefix("192.168.1.0/24"))
	} else {
		status.LocalNets = append(extra, hostNetworks()...)
	}

	var st *store.Store
	if demoMode {
		if st, err = store.Open(""); err != nil {
			return err
		}
		go func() { errc <- demo.Run(ctx, st) }()
	} else {
		if st, err = store.Open(dbPath); err != nil {
			return err
		}
		if err := startLive(ctx, st, status, ifaceFlag, pveDir, retention, errc); err != nil {
			st.Close()
			return err
		}
	}
	defer st.Close()

	password := os.Getenv("TATTLE_PASSWORD")
	srv := &http.Server{
		Addr:              listen,
		Handler:           api.New(st, status, ui.Files(), password),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	switch {
	case demoMode:
		log.Printf("demo mode: showing invented traffic")
	default:
		log.Printf("watching %s", strings.Join(status.Interfaces, ", "))
	}
	log.Printf("dashboard on http://%s", displayAddr(listen))
	if password == "" && !demoMode {
		log.Printf("anyone who can reach that address can see it; set TATTLE_PASSWORD to require a password")
	}

	select {
	case err := <-errc:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func displayAddr(listen string) string {
	if strings.HasPrefix(listen, ":") {
		name, err := os.Hostname()
		if err != nil {
			name = "localhost"
		}
		return name + listen
	}
	return listen
}

// startLive opens the captures and starts the background loops that feed
// the store.
func startLive(ctx context.Context, st *store.Store, status *api.Status, ifaceFlag, pveDir string, retention int, errc chan<- error) error {
	names, err := pickInterfaces(ifaceFlag)
	if err != nil {
		return err
	}
	var sources []*capture.Source
	for _, name := range names {
		src, err := capture.Open(name)
		if err != nil {
			return err
		}
		sources = append(sources, src)
	}
	status.Interfaces = names

	tr := tracker.New()
	reload := func() {
		machines, err := guests.Load(pveDir)
		if err != nil {
			log.Printf("could not read VM and container names: %v", err)
		}
		machines = append([]model.Machine{hostMachine(names)}, machines...)
		tr.SetMachines(machines)
		learned := tr.MachineIPs()
		for i := range machines {
			if ips := learned[machines[i].ID]; len(ips) > 0 {
				machines[i].IPs = ips
			}
		}
		if err := st.SaveMachines(machines); err != nil {
			log.Printf("saving machine list: %v", err)
		}
	}
	reload()

	for _, src := range sources {
		go func() {
			errc <- src.Run(ctx, func(frame []byte, wireLen int) {
				status.Packets.Add(1)
				if p, ok := decode.Decode(frame); ok {
					p.Truncated = wireLen > len(frame)
					tr.Handle(p, wireLen, time.Now())
				}
			})
		}()
	}

	go func() {
		flush := time.NewTicker(2 * time.Second)
		refresh := time.NewTicker(30 * time.Second)
		prune := time.NewTicker(time.Hour)
		defer flush.Stop()
		defer refresh.Stop()
		defer prune.Stop()
		cutoff := func() time.Time { return time.Now().AddDate(0, 0, -retention) }
		if err := st.Prune(cutoff()); err != nil {
			log.Printf("removing old history: %v", err)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-flush.C:
				if err := st.Apply(tr.Flush(now)); err != nil {
					log.Printf("saving traffic: %v", err)
				}
			case <-refresh.C:
				reload()
				for _, src := range sources {
					status.Dropped.Add(src.Dropped())
				}
			case <-prune.C:
				if err := st.Prune(cutoff()); err != nil {
					log.Printf("removing old history: %v", err)
				}
			}
		}
	}()
	return nil
}

// pickInterfaces returns the bridges to watch: the ones asked for, or every
// Proxmox bridge that is up.
func pickInterfaces(flagValue string) ([]string, error) {
	if flagValue != "" {
		var names []string
		for _, n := range strings.Split(flagValue, ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		return names, nil
	}
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var names, seen []string
	for _, ifi := range all {
		seen = append(seen, ifi.Name)
		if strings.HasPrefix(ifi.Name, "vmbr") && ifi.Flags&net.FlagUp != 0 {
			names = append(names, ifi.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no Proxmox bridge (vmbr*) found; pick one with --iface (available: %s) or use --demo",
			strings.Join(seen, ", "))
	}
	return names, nil
}

func parsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("--local-net %q is not a network like 10.20.30.0/24", s)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// hostNetworks returns the networks the host has an address on. If the host
// is 10.20.30.5/24, everything in 10.20.30.x is inside the home network.
func hostNetworks() []netip.Prefix {
	var out []netip.Prefix
	seen := map[netip.Prefix]bool{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		p, err := netip.ParsePrefix(ipn.String())
		if err != nil || p.Addr().IsLoopback() || p.Addr().IsLinkLocalUnicast() {
			continue
		}
		if p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked(); !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// hostMachine describes the Proxmox host itself, so its own traffic
// (updates, backups, the web panel) shows up next to the guests'.
func hostMachine(watched []string) model.Machine {
	name, err := os.Hostname()
	if err != nil {
		name = "Proxmox host"
	}
	m := model.Machine{ID: "host", Kind: "host", Name: name, Node: name, MACs: []string{}, IPs: []string{}}
	for _, w := range watched {
		if ifi, err := net.InterfaceByName(w); err == nil && len(ifi.HardwareAddr) == 6 {
			m.MACs = append(m.MACs, ifi.HardwareAddr.String())
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			m.IPs = append(m.IPs, ipn.IP.String())
		}
	}
	return m
}
