//go:build windows

package platform

// SyncDir is a no-op on Windows. There is no supported way to open a
// directory handle and fsync it as Unix does (CreateFile needs
// FILE_FLAG_BACKUP_SEMANTICS and FlushFileBuffers on a directory handle fails),
// so metadata durability is delegated to NTFS, which journals metadata
// operations. The residual difference is documented in docs/SAFETY.md: a
// rename is as durable as the filesystem journal, not explicitly flushed.
func SyncDir(string) error { return nil }
