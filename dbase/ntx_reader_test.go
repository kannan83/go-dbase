package dbase

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadOnlyNTXExactRecordNumbers(t *testing.T) {
	const keySize = uint16(6)
	itemSize := keySize + 8
	maxItem := uint16((ntxPageSize-2)/(itemSize+2) - 1)

	header := make([]byte, ntxPageSize)
	binary.LittleEndian.PutUint16(header[0:2], 0x0006)
	binary.LittleEndian.PutUint32(header[4:8], ntxPageSize)
	binary.LittleEndian.PutUint16(header[12:14], itemSize)
	binary.LittleEndian.PutUint16(header[14:16], keySize)
	binary.LittleEndian.PutUint16(header[18:20], maxItem)
	copy(header[22:], []byte("GZRCOD"))

	page := make([]byte, ntxPageSize)
	entries := []struct {
		key   string
		recno uint32
	}{
		{"123456", 1},
		{"123456", 7},
		{"850052", 3},
	}
	binary.LittleEndian.PutUint16(page[0:2], uint16(len(entries)))
	itemArea := 2 + 2*(int(maxItem)+1)
	for slot := 0; slot <= int(maxItem); slot++ {
		binary.LittleEndian.PutUint16(page[2+2*slot:], uint16(itemArea+slot*int(itemSize)))
	}
	for i, entry := range entries {
		off := itemArea + i*int(itemSize)
		binary.LittleEndian.PutUint32(page[off+4:off+8], entry.recno)
		copy(page[off+8:off+8+int(keySize)], []byte(entry.key))
	}

	path := filepath.Join(t.TempDir(), "gzr.ntx")
	if err := os.WriteFile(path, append(header, page...), 0600); err != nil {
		t.Fatal(err)
	}
	ix, err := openReadOnlyNTX(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	if ix.keyExpr != "GZRCOD" {
		t.Fatalf("key expression = %q, want GZRCOD", ix.keyExpr)
	}
	recnos, err := ix.exactRecordNumbers([]byte("123456"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recnos, []uint32{1, 7}) {
		t.Fatalf("record numbers = %v, want [1 7]", recnos)
	}

	recnos, err = ix.exactRecordNumbers([]byte("999999"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recnos) != 0 {
		t.Fatalf("missing key returned %v", recnos)
	}
}
