# AGENTS.md

Shirudo in a spin-machine guest, end to end. A demo: keep it small and plain.

## Layout

- `cmd/demo`: the host side. One step per method, listed in `main.go`'s `steps`.
- `cmd/demo-guest`: the guest side, run from the disk mounted at `/opt/demo`. Prints `## ` headings; the host keeps the output in `evidence/`.
- `internal/console`: types commands at the guest's serial console and reads their exit status.
- `versions.yaml`: every pin (spin-machine release and its `shirudo-operator` tarball by sha256, the registry commit, Task). Compiled into `demo` via `embed.go`.
- `evidence/`: what one full run printed. Committed.

Everything shirudo needs comes from the spin-machine release. Nothing is built from source and nothing runs in Docker: the `operator` step runs the release's operator half in a throwaway guest.

## Commands

```sh
go tool task test                  # gofmt, vet, test, build: the CI Gate
go tool task                       # the whole demo (needs KVM; ~5 min)
go tool task clean && go tool task # from scratch; required after a versions.yaml bump
go tool task <step>                # one step and what it needs
```

A change to the demo's behavior is verified by a full run, and its `evidence/` is committed with it.

## Conventions

- Comments only for what the code cannot say: a behavior outside this repo (QEMU, passt, the kernel, a tty). No doc-comment essays.
- Standard library first. The only direct dependencies are go-tools and `golang.org/x/sys`.
- Bump a pin in `versions.yaml` and nowhere else; `shirudo-operator` moves with `spin-machine`.
- Commit subjects say what changed, in plain words; the body says why and what the evidence run showed.
