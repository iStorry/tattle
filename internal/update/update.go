// Package update fetches a Tattle release from GitHub and checks it against
// the published checksums, the same way install.sh does.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBaseURL is where Tattle's releases are published.
const DefaultBaseURL = "https://github.com/istorry/tattle"

// maxDownload is far more than a package will ever be; it only stops a
// broken or hostile server from filling the disk.
const maxDownload = 200 << 20

// ErrNoRelease means GitHub was reached but has no full release, only
// drafts or pre-releases, which never count as the latest.
var ErrNoRelease = errors.New("there is no full release yet")

// Source is where releases are downloaded from.
type Source struct {
	BaseURL string // DefaultBaseURL, or a mirror
	Client  *http.Client
}

// New returns a Source for baseURL, or for GitHub if it is "". Downloads
// are over HTTPS only, unless baseURL itself is plain HTTP, which is for
// testing.
func New(baseURL string) *Source {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	httpsOnly := !strings.HasPrefix(baseURL, "http://")
	return &Source{BaseURL: baseURL, Client: &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if httpsOnly && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing a redirect to %s, which is not HTTPS", req.URL.Host)
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

func (s *Source) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tattle-update")
	res, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s answered %s", url, res.Status)
	}
	return res, nil
}

// Latest returns the tag of the newest full release, such as "v0.1.0".
// GitHub answers /releases/latest with a redirect to it, which needs no API
// and no login.
func (s *Source) Latest(ctx context.Context) (string, error) {
	res, err := s.get(ctx, s.BaseURL+"/releases/latest")
	if err != nil {
		return "", err
	}
	res.Body.Close()
	_, tag, ok := strings.Cut(res.Request.URL.Path, "/releases/tag/")
	if !ok || tag == "" || strings.Contains(tag, "/") {
		// With no full release GitHub redirects to the list instead.
		return "", ErrNoRelease
	}
	return tag, nil
}

// PackageName is the file a release publishes for a kind of processor.
func PackageName(tag, arch string) string {
	return fmt.Sprintf("tattle_%s_%s.deb", strings.TrimPrefix(tag, "v"), arch)
}

// Download fetches the package of release tag for arch into dir and returns
// its path. It fails, leaving nothing to install, unless the file is
// exactly the one the release's checksums describe.
func (s *Source) Download(ctx context.Context, tag, arch, dir string) (string, error) {
	file := PackageName(tag, arch)
	base := s.BaseURL + "/releases/download/" + tag + "/"

	res, err := s.get(ctx, base+"SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("could not download the checksums for release %s, so the package cannot be verified: %w", tag, err)
	}
	want, err := checksumFor(io.LimitReader(res.Body, 1<<20), file)
	res.Body.Close()
	if err != nil {
		return "", err
	}

	res, err = s.get(ctx, base+file)
	if err != nil {
		return "", fmt.Errorf("could not download %s from release %s: %w", file, tag, err)
	}
	defer res.Body.Close()
	path := filepath.Join(dir, file)
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), io.LimitReader(res.Body, maxDownload+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownload {
		err = errors.New("the download is far larger than a Tattle package")
	}
	if err == nil && hex.EncodeToString(sum.Sum(nil)) != want {
		err = errors.New("the download does not match its published checksum; try again, and if it happens twice, please report it")
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// checksumFor finds file's SHA-256 in a SHA256SUMS listing.
func checksumFor(sums io.Reader, file string) (string, error) {
	sc := bufio.NewScanner(sums)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// "<hash>  <name>", or "<hash> *<name>" for binary mode.
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == file && len(fields[0]) == sha256.Size*2 {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("the published checksums do not mention %s, so the package cannot be verified", file)
}
