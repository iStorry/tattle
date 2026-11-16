package guests

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "nodes/pve/qemu-server/100.conf"), `# a comment
boot: order=scsi0
name: home-assistant
net0: virtio=BC:24:11:2E:5A:01,bridge=vmbr0,firewall=1
net1: e1000=BC:24:11:2E:5A:02,bridge=vmbr1,tag=20

[before-upgrade]
name: old-name
net0: virtio=AA:AA:AA:AA:AA:AA,bridge=vmbr0
`)
	write(t, filepath.Join(dir, "nodes/pve/qemu-server/900.conf"), "name: debian-template\ntemplate: 1\nnet0: virtio=BC:24:11:00:00:99,bridge=vmbr0\n")
	write(t, filepath.Join(dir, "nodes/pve/lxc/200.conf"), `arch: amd64
hostname: pihole
net0: name=eth0,bridge=vmbr0,gw=192.168.1.1,hwaddr=BC:24:11:AA:BB:CC,ip=192.168.1.53/24,ip6=dhcp,type=veth
`)
	write(t, filepath.Join(dir, "nodes/pve2/lxc/201.conf"), "net0: name=eth0,bridge=vmbr0,hwaddr=BC:24:11:AA:BB:CD,ip=dhcp,type=veth\n")
	write(t, filepath.Join(dir, "nodes/pve/lxc/notes.conf"), "hostname: ignored\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d machines: %+v", len(got), got)
	}
	vm, ct, other := got[0], got[1], got[2]
	if vm.ID != "vm-100" || vm.Kind != "vm" || vm.Name != "home-assistant" || vm.Node != "pve" ||
		!reflect.DeepEqual(vm.MACs, []string{"bc:24:11:2e:5a:01", "bc:24:11:2e:5a:02"}) || len(vm.IPs) != 0 {
		t.Errorf("vm = %+v", vm)
	}
	if ct.ID != "ct-200" || ct.Name != "pihole" || !reflect.DeepEqual(ct.MACs, []string{"bc:24:11:aa:bb:cc"}) ||
		!reflect.DeepEqual(ct.IPs, []string{"192.168.1.53"}) {
		t.Errorf("container = %+v", ct)
	}
	if other.ID != "ct-201" || other.Node != "pve2" || other.Name != "CT 201" {
		t.Errorf("unnamed container on another node = %+v", other)
	}
}

func TestLoadMissingDirectory(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing config directory")
	}
}
