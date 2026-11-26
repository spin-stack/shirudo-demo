// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	cveID      = "cve-2025-24813"
	cvePackage = stage + "/packages/" + cveID + "-1.0.0.bee"
	registry   = stage + "/registry"
)

func cve() error {
	dir := filepath.Join(work, cveID)
	up, unload, err := unpackCVE(dir)
	if err != nil {
		return err
	}
	envFile := filepath.Join(dir, "env")
	env, err := readEnv(envFile)
	if err == nil && tomcatAnswers() {
		heading("setup: Tomcat already runs")
		_ = exec.Command(shirudo, "upatch", "unload", unload).Run()
	} else {
		heading("setup, the registry's, unchanged")
		var stdout bytes.Buffer
		cmd := exec.Command(filepath.Join(registry, "payloads", cveID, "setup"))
		cmd.Stdout, cmd.Stderr = &stdout, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
		if err := os.WriteFile(envFile, stdout.Bytes(), 0o644); err != nil {
			return err
		}
		if env, err = readEnv(envFile); err != nil {
			return err
		}
	}
	fmt.Print(strings.Join(env, "\n") + "\n")

	payload := func(title string) int {
		heading("payload, %s", title)
		rc := status(run(env, filepath.Join(registry, "payloads", cveID, "payload")))
		fmt.Println("payload exit", rc)
		return rc
	}
	before := payload("before the upatch")
	heading("shirudo upatch load %s", filepath.Base(up))
	if err := run(nil, shirudo, "upatch", "load", up); err != nil {
		return err
	}
	heading("shirudo upatch list")
	if err := run(nil, shirudo, "upatch", "list"); err != nil {
		return err
	}
	loaded := payload("upatch loaded")
	heading("shirudo upatch unload %s", filepath.Base(unload))
	if err := run(nil, shirudo, "upatch", "unload", unload); err != nil {
		return err
	}
	after := payload("after the unload")
	heading("result: before=%d loaded=%d after=%d", before, loaded, after)
	if before != 0 || loaded != 10 || after != 0 {
		return errors.New("the payload did not exit 0, 10, 0")
	}
	return nil
}

func cveTest() error {
	env, err := readEnv(filepath.Join(work, cveID, "env"))
	if err != nil {
		return fmt.Errorf("run cve first: %w", err)
	}
	cmd := exec.Command(filepath.Join(registry, "tools/test"), cvePackage)
	cmd.Dir = registry
	cmd.Env = append(append(os.Environ(), env...), "SHIRUDO="+shirudo)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

func unpackCVE(dir string) (up, unload string, err error) {
	ups, err := unpack(cvePackage, dir)
	if err != nil {
		return "", "", err
	}
	for _, f := range ups {
		if !strings.HasSuffix(f, ".unload.upatch") {
			up = f
		}
	}
	if up == "" {
		return "", "", fmt.Errorf("%s holds no upatch: %v", cvePackage, ups)
	}
	return up, strings.TrimSuffix(up, ".upatch") + ".unload.upatch", nil
}

func readEnv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var env []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, _, ok := strings.Cut(sc.Text(), "="); ok && k != "" && k == strings.ToUpper(k) {
			env = append(env, sc.Text())
		}
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("%s names no variable", path)
	}
	return env, sc.Err()
}

func tomcatAnswers() bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:8080/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func unpack(archive, dir string) ([]string, error) {
	pkg := filepath.Join(dir, "package")
	if err := os.RemoveAll(pkg); err != nil {
		return nil, err
	}
	if err := untar(archive, pkg); err != nil {
		return nil, err
	}
	return filepath.Glob(filepath.Join(pkg, "*", "upatches", "*.upatch"))
}

func untar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(h.Name) {
			return fmt.Errorf("%s: %q leaves the directory", archive, h.Name)
		}
		to := filepath.Join(dir, h.Name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, h.FileInfo().Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: %q is neither a file nor a directory", archive, h.Name)
		}
	}
}
