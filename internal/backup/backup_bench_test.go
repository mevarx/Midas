package backup

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkBackupSetVerify(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, "app", "set")
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		b.Fatal(err)
	}
	var files []File
	data := make([]byte, 16<<10)
	for i := range data {
		data[i] = byte(i * 17)
	}
	for i := range 128 {
		rel := fmt.Sprintf("item-%03d", i)
		if err := os.WriteFile(filepath.Join(filesDir, rel), data, 0o600); err != nil {
			b.Fatal(err)
		}
		files = append(files, File{Path: rel, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	set := &Set{Schema: SchemaVersion, ID: "set", App: "app", Target: "target", State: StateVerified, Dir: dir, Files: files}
	b.ReportAllocs()
	b.SetBytes(int64(len(files) * len(data)))
	b.ReportMetric(float64(runtime.NumCPU()), "host-cpus")
	b.Logf("os=%s arch=%s go=%s workers=sequential", runtime.GOOS, runtime.GOARCH, runtime.Version())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Verify(set); err != nil {
			b.Fatal(err)
		}
	}
}
