#!/bin/sh
set -eu

echo "== workload host =="
date -u '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || true
uname -a
go version
go env GOOS GOARCH
echo "GOMAXPROCS=${GOMAXPROCS:-runtime default}"
if command -v nproc >/dev/null 2>&1; then
	echo "logical_cpus=$(nproc)"
elif command -v sysctl >/dev/null 2>&1; then
	echo "logical_cpus=$(sysctl -n hw.ncpu)"
fi
if command -v lscpu >/dev/null 2>&1; then
	lscpu | grep -E '^(Model name|CPU\(s\)|Architecture):' || true
fi
if command -v free >/dev/null 2>&1; then
	free -h
elif command -v sysctl >/dev/null 2>&1; then
	sysctl -n hw.memsize 2>/dev/null || true
fi
df -T "${TMPDIR:-/tmp}" 2>/dev/null || df -h "${TMPDIR:-/tmp}"

echo "== benchmarks =="
if /usr/bin/time -v true >/dev/null 2>&1; then
	/usr/bin/time -v go test ./internal/mover ./internal/backup -run '^$' \
		-bench 'Benchmark(CopyVerify|CrossVolumeCopy|SameVolumeMove|BackupSetVerify)$' -benchmem -count=5
else
	time go test ./internal/mover ./internal/backup -run '^$' \
		-bench 'Benchmark(CopyVerify|CrossVolumeCopy|SameVolumeMove|BackupSetVerify)$' -benchmem -count=5
fi

if command -v lsof >/dev/null 2>&1; then
	echo "lsof is available for separate peak-descriptor sampling when profiling individual benchmark processes"
else
	echo "lsof unavailable; per-operation descriptor peak not measured"
fi
