# Performance and resource methodology

## Copy behavior

`midas move|resume|restore|rollback --copy-workers N` supports a bounded
worker count from 1 to 16. The default remains **1**. Workers copy regular
files only; directory creation and link recreation remain ordered, symlinks are
never followed, case collisions are rejected before file writes, and all
workers stop before verification or journal advancement. Queue capacity is at
most twice the worker count. Each active copy uses one 1 MiB buffer and at most
two file handles, in addition to the tree walk and operating-system overhead.
File-path metadata and case-collision tracking are proportional to the source
tree size.

Parallel copying is opt-in because small-file performance can regress on some
systems. The benchmark fixture compares sequential and four-worker copies for
many small files and fewer large files, with basic and SHA-256 verification,
plus backup-set verification.

## Reproduce measurements

Run on an otherwise idle machine and record CPU model/count, memory, operating
system, architecture, Go version, filesystem type, and whether storage is local,
external, encrypted, or virtualized:

```sh
sh scripts/benchmark.sh
```

The script records OS, architecture, Go version, logical CPU count, and the
filesystem backing the temp directory. GNU `/usr/bin/time -v` reports peak RSS
where available. The benchmarks report elapsed time, throughput, and Go
allocation metrics.
They include tree creation only in fixture setup, not in the timed section.
`BenchmarkCrossVolumeCopy` uses `/dev/shm` when it is a distinct filesystem or
`MIDAS_BENCH_CROSS_VOLUME_DEST`; otherwise it is skipped.
`BenchmarkSameVolumeMove` times the move lifecycle and removes its fixture
outside the timed region.
Cache state, filesystem, and device performance strongly affect results; these
measurements are not universal performance claims. Same-volume moves use an
atomic rename. Cross-volume copy throughput should be measured on
representative source and destination devices; the benchmark does not simulate
a slow or removable volume.

## Space and progress

Move preflight estimates logical source and backup bytes, adds 10% overhead,
accounts for same-volume rename and shared backup/vault volumes, and checks
available space. Filesystem allocation, metadata, concurrent writers, and
changes after preflight make the estimate incomplete; it is not a reservation.
Copy and verification have separate progress phases with bytes, files, elapsed
time, rate, and a best-effort ETA. Progress is observational and does not
control state transitions.

## Current evidence

Benchmark results are machine-specific. Attach raw `go test -bench` output and
the environment details to any proposal to change the default worker count.
This PR keeps sequential copying as the default; its single-host benchmark
results are not sufficient to establish a cross-platform default improvement.
