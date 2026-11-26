// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spin-stack/go-tools/sigcheck"
	"github.com/spin-stack/go-tools/versions"
)

// The workflow that signs a spin-machine release, at the ref its runs
// publish from (spin-stack/machine docs/releasing.md, "Verify a release file").
const (
	machineWorkflow = "https://github.com/spin-stack/machine/.github/workflows/release.yml"
	machineRef      = "refs/heads/main"
)

// releaseName is the tarball's top directory, and the name it is unpacked under.
func releaseName(e versions.Entry) string {
	return "spin-machine-" + e.Version + "-linux-x86_64"
}

// fetchRelease downloads the release versions.yaml pins, checks it against
// the pin and against its Sigstore bundle, and unpacks it in work/machine.
// The tree appears only whole and checked, so its presence is the proof.
func (d *demo) fetchRelease() error {
	e := d.pins.machine
	name := releaseName(e)
	dir := filepath.Join(d.work, "machine", name)
	if _, err := os.Stat(d.machineCmd); err == nil {
		say("release %s: unpacked in %s", e.Version, dir)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tarball := dir + ".tar.gz"
	say("release %s: downloading %s", e.Version, e.URL())
	sum, err := download(e.URL(), tarball)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(sum); got != e.Pin {
		return fmt.Errorf("%s: sha256 %s, versions.yaml pins %s", e.URL(), got, e.Pin)
	}
	say("release %s: sha256 %s, as pinned", e.Version, e.Pin)
	var bundle bytes.Buffer
	if err := get(e.URL()+".sigstore.json", &bundle); err != nil {
		return err
	}
	policy, err := sigcheck.Workflows(machineRef, machineWorkflow)
	if err != nil {
		return err
	}
	if err := policy.Check(bundle.Bytes(), sum); err != nil {
		return fmt.Errorf("%s.sigstore.json: %w", e.URL(), err)
	}
	say("release %s: signed by %s@%s", e.Version, machineWorkflow, machineRef)
	tmp := dir + ".unpacking"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := cmd("", "tar", "-xzf", tarball, "-C", tmp); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(tmp, name), dir); err != nil {
		return err
	}
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	return os.Remove(tarball)
}

// download writes url to path and returns its sha256.
func download(url, path string) ([]byte, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	err = get(url, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	return h.Sum(nil), nil
}

func get(url string, w io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
