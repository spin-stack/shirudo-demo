// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// lookup is the name the guest resolves and connects to, through passt's
// DNS and NIC.
const lookup = "upatch.dev"

var tables = []string{"exec", "file", "dns", "net"}

// obs loads shirudo's kernel-obs package, has the kernel run a program, open
// a credential file, resolve a name and make two connections, and reads the
// records back with `shirudo upatch map`, in the guest, with no collector.
func obs() error {
	dir := filepath.Join(work, "kernel-obs")
	ups, err := unpack(stage+"/packages/kernel-obs-1.0.0.bee", dir)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ups, func(f string) bool { return filepath.Base(f) == "kernel-obs.upatch" })
	if i < 0 {
		return fmt.Errorf("kernel-obs package holds no kernel-obs.upatch: %v", ups)
	}
	list, err := exec.Command(shirudo, "upatch", "list").Output()
	if err != nil {
		return fmt.Errorf("shirudo upatch list: %w", err)
	}
	if !regexp.MustCompile(`(?m)^\s*kernel-obs\s+yes\s`).Match(list) {
		heading("shirudo upatch load kernel-obs.upatch")
		if err := run(nil, shirudo, "upatch", "load", ups[i]); err != nil {
			return err
		}
	}
	heading("shirudo upatch list")
	if err := run(nil, shirudo, "upatch", "list"); err != nil {
		return err
	}

	// Each read drains the table's interval, so the actions run right after a
	// read of every table, and the next read holds what they did.
	for _, t := range tables {
		if _, err := mapTable(t); err != nil {
			return err
		}
	}
	id, err := filepath.EvalSymlinks("/usr/bin/id")
	if err != nil {
		return err
	}
	if err := act(id); err != nil {
		return err
	}

	records := map[string][]map[string]any{}
	for _, t := range tables {
		heading("shirudo upatch map kernel-obs %s --json", t)
		if records[t], err = mapTable(t); err != nil {
			return err
		}
		summarize(records[t])
	}
	heading("shirudo upatch map kernel-obs")
	if err := run(nil, shirudo, "upatch", "map", "kernel-obs"); err != nil {
		return err
	}

	heading("checks")
	var failed []string
	check := func(table, what string, ok func(r map[string]any) bool) {
		if slices.ContainsFunc(records[table], ok) {
			fmt.Printf("%s: %s recorded\n", table, what)
		} else {
			failed = append(failed, table+": no "+what)
		}
	}
	check("exec", "/usr/bin/id ("+id+")", func(r map[string]any) bool { return r["path"] == id })
	check("file", "/etc/shadow", func(r map[string]any) bool { return r["path"] == "/etc/shadow" })
	check("dns", lookup, func(r map[string]any) bool {
		names, _ := r["names"].([]any)
		return slices.ContainsFunc(names, func(n any) bool { m, _ := n.(map[string]any); return m["name"] == lookup })
	})
	check("net", "a connection to 127.0.0.1:8080", func(r map[string]any) bool {
		return r["role"] == "initiator" && r["address"] == "127.0.0.1" && r["port"] == 8080.0
	})
	check("net", "a connection to "+lookup+":443", func(r map[string]any) bool {
		return r["role"] == "initiator" && r["port"] == 443.0 && r["address"] != "127.0.0.1"
	})
	if failed != nil {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// act makes the kernel do what kernel-obs records.
func act(id string) error {
	heading("an exec: /usr/bin/id")
	if err := run(nil, id); err != nil {
		return err
	}

	heading("a read of /etc/shadow")
	f, err := os.Open("/etc/shadow")
	if err != nil {
		return err
	}
	_, err = f.Read(make([]byte, 1))
	f.Close()
	if err != nil {
		return err
	}
	fmt.Println("read")

	heading("a DNS lookup: %s, through the guest's resolver", lookup)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := net.Resolver{PreferGo: true}
	addrs, err := r.LookupIPAddr(ctx, lookup)
	if err != nil {
		return err
	}
	ips := make([]string, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP.String()
	}
	fmt.Println(lookup, "->", strings.Join(ips, " "))

	heading("a connection: 127.0.0.1:8080")
	if err := connect("127.0.0.1:8080", "GET / HTTP/1.0\r\n\r\n"); err != nil {
		fmt.Println("nothing answers on 8080, run cve first:", err)
	}

	heading("a connection: %s:443", lookup)
	return connect(net.JoinHostPort(addrs[0].IP.String(), "443"), "")
}

func connect(addr, send string) error {
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Println("connected to", c.RemoteAddr())
	if send == "" {
		return nil
	}
	if _, err := c.Write([]byte(send)); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b := make([]byte, 12)
	n, err := c.Read(b)
	fmt.Printf("%q\n", b[:n])
	return err
}

func mapTable(table string) ([]map[string]any, error) {
	out, err := exec.Command(shirudo, "upatch", "map", "kernel-obs", table, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("shirudo upatch map kernel-obs %s: %w", table, err)
	}
	var doc struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("kernel-obs %s: %w", table, err)
	}
	return doc.Records, nil
}

// noise is what summarize leaves out of a record: identities and times.
var noise = []string{"entry_index", "cgroup_id", "executable", "caller", "owner", "actor",
	"first_seen_unix_nano", "last_seen_unix_nano", "cap_effective", "path_truncated"}

// summarize prints one line per record, without its identity and time fields.
func summarize(records []map[string]any) {
	fmt.Println(len(records), "records")
	for _, r := range records {
		shown := maps.Clone(r)
		for _, k := range noise {
			delete(shown, k)
		}
		b, _ := json.Marshal(shown)
		fmt.Println(" ", string(b))
	}
}
