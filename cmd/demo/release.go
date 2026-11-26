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
	"path"
	"path/filepath"

	"github.com/spin-stack/go-tools/sigcheck"
	"github.com/spin-stack/go-tools/versions"
)

const (
	machineWorkflow = "https://github.com/spin-stack/machine/.github/workflows/release.yml"
	machineRef      = "refs/heads/main"
)

func releaseName(e versions.Entry) string {
	return "spin-machine-" + e.Version + "-linux-x86_64"
}

func (d *demo) fetchRelease() error {
	e := d.pins.machine
	name := releaseName(e)
	dir := filepath.Join(d.work, "machine", name)
	if _, err := os.Stat(d.machineCmd); err == nil {
		say("release %s: unpacked in %s", e.Version, dir)
		return nil
	}
	tmp := dir + ".unpacking"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	for _, t := range []versions.Entry{e, d.pins.operator} {
		if err := fetch(t, tmp); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(tmp, name), dir); err != nil {
		return err
	}
	return os.RemoveAll(tmp)
}

func fetch(e versions.Entry, dir string) error {
	tarball := filepath.Join(dir, path.Base(e.URL()))
	say("%s %s: downloading %s", e.Name, e.Version, e.URL())
	sum, err := download(e.URL(), tarball)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(sum); got != e.Pin {
		return fmt.Errorf("%s: sha256 %s, versions.yaml pins %s", e.URL(), got, e.Pin)
	}
	say("%s %s: sha256 %s, as pinned", e.Name, e.Version, e.Pin)
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
	say("%s %s: signed by %s@%s", e.Name, e.Version, machineWorkflow, machineRef)
	if err := cmd("", "tar", "-xzf", tarball, "-C", dir); err != nil {
		return err
	}
	return os.Remove(tarball)
}

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
