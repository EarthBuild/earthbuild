# EarthBuild failure diagnostics

Dumps host, memory, swap and buildkit daemon state after a failed EarthBuild
step.

Its reason to exist: `Canceled`, `file already closed` and a lost solve session
say the build stopped, not why. A download dying mid-transfer, a target failing
and cancelling its siblings, a daemon crash, an OOM-killed `buildkit-runc`
child - any of them land on the same message, and which one you have depends on
your build. Telling them apart needs the daemon log, `free` and `dmesg`, none of
which the job log gives you. This collects them.

## Usage

```yaml
      - name: Run the build
        run: earth --ci -P +test
      - name: Failure diagnostics
        if: ${{ failure() }}
        uses: EarthBuild/earthbuild/.github/actions/failure-diagnostics@main
```

Every input has a working default, so the snippet above is the whole
integration. Pin `@<sha>` rather than `@main` if you pin your other actions.

## Inputs

`BINARY` — container engine to inspect. Default `auto`, which tries `docker`,
`podman` and `nerdctl` in that order.

`SUDO` — prefix for engine commands when the daemon needs root, for example
`sudo -E`. Default empty.

`CONTAINERS` — space-separated buildkit container names to inspect. Default
`earthly-buildkitd earthly-dev-buildkitd earthly-integration-buildkitd
earthly-test-buildkitd`. Override it when you build with a custom
`DEFAULT_INSTALLATION_NAME`, which renames the daemon container.

`LOG_TAIL` — lines of daemon log dumped per container. Default `2000`.

`EXTRA_LOGS` — space-separated extra files to `cat`. Missing files are
skipped. Default empty. `$RUNNER_TEMP/add-swap.log` is always included, so a
runner that sets up swap before the build can leave its log there and have it
picked up without configuration.

## What it prints

Each section is a collapsed log group:

- host memory, disk and pressure — `df`, `uptime`, `free`, `swapon`, PSI
- extra logs — `EXTRA_LOGS`, plus a swap-setup log if the runner left one
- top processes — by RSS, with `oom_score` per pid
- kernel oom and cgroup messages — filtered `dmesg` (Linux only)
- container engine state — `version`, `info`, `ps -a`, `stats`
- buildkit containers — `inspect` and log tail for each name in `CONTAINERS`
- earth directories — cache sizes under `~/.earthly`

## Behaviour

- Always exits `0`. Diagnostics must never mask, or replace, the failure that
  triggered them.
- Linux-only probes (`/proc`, `dmesg`, GNU `ps`) are skipped on macOS runners
  rather than left to fail noisily; `vm_stat` and BSD `ps` stand in.
- Skips the engine and buildkit sections with a warning when no container
  engine is installed.
