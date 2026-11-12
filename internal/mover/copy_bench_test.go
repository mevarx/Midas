package mover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mevarx/Midas/internal/apps"
	"github.com/mevarx/Midas/internal/manifest"
	"github.com/mevarx/Midas/internal/platform"
)

func BenchmarkCopyVerify(b *testing.B) {
	workloads := []struct {
		name  string
		files int
		size  int
	}{
		{name: "many-small", files: 256, size: 4096},
		{name: "few-large", files: 8, size: 2 << 20},
	}
	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			root := b.TempDir()
			source := filepath.Join(root, "source")
			if err := os.MkdirAll(filepath.Join(source, "nested", "deep", "deeper"), 0o700); err != nil {
				b.Fatal(err)
			}
			data := make([]byte, workload.size)
			for i := range data {
				data[i] = byte(i * 31)
			}
			for i := range workload.files {
				name := filepath.Join(source, "nested", "deep", "deeper", fmt.Sprintf("%06d.dat", i))
				if err := os.WriteFile(name, data, 0o600); err != nil {
					b.Fatal(err)
				}
			}
			if runtime.GOOS != "windows" {
				if err := os.Symlink("nested", filepath.Join(source, "alias")); err != nil {
					b.Fatal(err)
				}
			}
			for _, workers := range []int{1, 4} {
				for _, hash := range []bool{false, true} {
					name := fmt.Sprintf("workers=%d/hash=%t", workers, hash)
					b.Run(name, func(b *testing.B) {
						b.ReportAllocs()
						b.SetBytes(int64(workload.files * workload.size))
						b.ReportMetric(float64(runtime.NumCPU()), "host-cpus")
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							dest := filepath.Join(root, fmt.Sprintf("dest-%d-%d-%t", i, workers, hash))
							if _, err := CopyTree(source, dest, CopyOptions{Workers: workers}); err != nil {
								b.Fatal(err)
							}
							if _, err := VerifyTree(source, dest, hash); err != nil {
								b.Fatal(err)
							}
							if err := os.RemoveAll(dest); err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

func BenchmarkCrossVolumeCopy(b *testing.B) {
	sourceRoot := b.TempDir()
	candidates := []string{os.Getenv("MIDAS_BENCH_CROSS_VOLUME_DEST"), "/dev/shm"}
	destRoot := ""
	for _, candidate := range candidates {
		if candidate == "" || !benchmarkDirExists(candidate) {
			continue
		}
		same, err := SameVolume(sourceRoot, candidate)
		if err == nil && !same {
			destRoot = candidate
			break
		}
	}
	if destRoot == "" {
		b.Skip("set MIDAS_BENCH_CROSS_VOLUME_DEST to an available different filesystem")
	}
	source := filepath.Join(sourceRoot, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		b.Fatal(err)
	}
	data := make([]byte, 1<<20)
	for i := range 8 {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("%02d", i)), data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, workers := range []int{1, 4} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data) * 8))
			for i := 0; i < b.N; i++ {
				base, err := os.MkdirTemp(destRoot, "midas-copy-bench-")
				if err != nil {
					b.Fatal(err)
				}
				dest := filepath.Join(base, "copy")
				if _, err := CopyTree(source, dest, CopyOptions{Workers: workers, Context: context.Background()}); err != nil {
					b.Fatal(err)
				}
				if _, err := VerifyTree(source, dest, false); err != nil {
					b.Fatal(err)
				}
				if err := os.RemoveAll(base); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSameVolumeMove(b *testing.B) {
	root := b.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o700); err != nil {
		b.Fatal(err)
	}
	data := make([]byte, 64<<10)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.Logf("os=%s arch=%s go=%s cpus=%d", runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		source := filepath.Join(root, fmt.Sprintf("source-%d", i))
		if err := os.Mkdir(source, 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "data"), data, 0o600); err != nil {
			b.Fatal(err)
		}
		dest := filepath.Join(vault, "apps", "bench", "target")
		same, err := SameVolume(source, vault)
		if err != nil || !same {
			b.Skip("vault and source are not on the same volume")
		}
		b.StartTimer()
		_, err = MoveTarget(MoveOptions{
			App: "bench", Target: apps.Target{Name: "target"}, Source: source,
			Dest: dest, Vault: vault, NoBackupConfirmed: true, Manifest: manifest.Empty(),
		})
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		if err := platform.Current().RemoveLink(source); err != nil {
			b.Fatal(err)
		}
		if err := os.RemoveAll(dest); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkDirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
