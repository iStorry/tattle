package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// release serves one release, v1.2.3, the way GitHub lays it out.
func release(t *testing.T, deb, sums string, full bool) *Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if full {
			http.Redirect(w, r, "/releases/tag/v1.2.3", http.StatusFound)
		} else {
			http.Redirect(w, r, "/releases", http.StatusFound)
		}
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/releases/tag/v1.2.3", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/releases/download/v1.2.3/tattle_1.2.3_amd64.deb", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, deb)
	})
	mux.HandleFunc("/releases/download/v1.2.3/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func sumLine(content, name string) string {
	return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(content)), name)
}

func TestLatestFollowsTheRedirectToTheTag(t *testing.T) {
	tag, err := release(t, "", "", true).Latest(context.Background())
	if err != nil || tag != "v1.2.3" {
		t.Errorf("got %q, %v", tag, err)
	}
}

func TestLatestSaysWhenThereIsNoFullRelease(t *testing.T) {
	if _, err := release(t, "", "", false).Latest(context.Background()); !errors.Is(err, ErrNoRelease) {
		t.Errorf("got %v", err)
	}
}

func TestDownloadKeepsAPackageThatMatches(t *testing.T) {
	sums := sumLine("other", "tattle_1.2.3_arm64.deb") + sumLine("the package", "tattle_1.2.3_amd64.deb")
	path, err := release(t, "the package", sums, true).Download(context.Background(), "v1.2.3", "amd64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "the package" {
		t.Errorf("saved %q", got)
	}
}

func TestDownloadRefusesAPackageThatDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	sums := sumLine("the package", "tattle_1.2.3_amd64.deb")
	_, err := release(t, "something else", sums, true).Download(context.Background(), "v1.2.3", "amd64", dir)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("got %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the bad download was left behind: %v", left)
	}
}

func TestDownloadRefusesAPackageWithNoChecksum(t *testing.T) {
	sums := sumLine("the package", "tattle_1.2.3_arm64.deb")
	_, err := release(t, "the package", sums, true).Download(context.Background(), "v1.2.3", "amd64", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "do not mention") {
		t.Errorf("got %v", err)
	}
}
