package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/istorry/tattle/internal/update"
)

// runUpdate replaces the installed package with the latest release, or with
// the release named. It does what install.sh does, without needing curl or
// the script: download the package for this machine from GitHub, check it
// against the published checksums, and hand it to apt.
func runUpdate(want string) error {
	if runtime.GOOS != "linux" {
		return errors.New("update installs a Debian package, so it only works on a Proxmox or other Debian-based host")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("there is no package for this kind of processor (%s); only amd64 and arm64 are built", arch)
	}
	if os.Geteuid() != 0 {
		return errors.New("run this as root (on Proxmox, the root shell is what you have by default)")
	}
	apt, err := exec.LookPath("apt-get")
	if err != nil {
		return errors.New("apt was not found; update is for Proxmox and other Debian-based systems")
	}
	// Only a copy installed from the package can be replaced by one.
	if err := exec.Command("dpkg-query", "--show", "tattle").Run(); err != nil {
		return errors.New("Tattle was not installed from its package here, so there is nothing for apt to update; see the README for how to install it")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	src := update.New(os.Getenv("TATTLE_BASE_URL"))

	tag := want
	if tag == "" {
		tag, err = src.Latest(ctx)
		switch {
		case errors.Is(err, update.ErrNoRelease):
			return fmt.Errorf("there is no full release of Tattle yet, only pre-releases. To install one, name it, such as: tattle update v0.1.0-rc1. See %s/releases", src.BaseURL)
		case err != nil:
			return fmt.Errorf("could not find the latest release: %w", err)
		}
		if tag == version {
			fmt.Printf("Tattle %s is the latest release. Nothing to do.\n", strings.TrimPrefix(tag, "v"))
			return nil
		}
	} else if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}

	tmp, err := os.MkdirTemp("", "tattle-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	// apt reads the file as its own unprivileged user.
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}

	fmt.Printf("Downloading Tattle %s for %s and checking it.\n", strings.TrimPrefix(tag, "v"), arch)
	deb, err := src.Download(ctx, tag, arch, tmp)
	if err != nil {
		return err
	}

	fmt.Println("Installing.")
	cmd := exec.CommandContext(ctx, apt, "install", "--yes", "--quiet", deb)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return errors.New("apt could not install the package; its messages are above")
	}
	fmt.Printf("\nTattle was updated from %s to %s. Your history, settings and password are unchanged.\n",
		strings.TrimPrefix(version, "v"), strings.TrimPrefix(tag, "v"))
	return nil
}
