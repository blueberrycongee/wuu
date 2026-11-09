package session

// StorageCompactionResult reports allocated database pages in bytes. These
// counts exclude temporary files and the write-ahead log.
type StorageCompactionResult struct {
	DatabaseBytesBefore int64 `json:"database_bytes_before"`
	DatabaseBytesAfter  int64 `json:"database_bytes_after"`
}

type storagePages struct {
	pageSize   int64
	pageCount  int64
	freePages  int64
	autoVacuum int
}
