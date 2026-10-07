package dbase

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

const ntxPageSize = 1024

type ntxEntry struct {
	key   []byte
	recno uint32
}

type ntxItem struct {
	child int64
	recno uint32
	key   []byte
}

type ntxNode struct {
	items []ntxItem
	right int64
	leaf  bool
}

type readOnlyNTX struct {
	file     *os.File
	root     int64
	itemSize uint16
	keySize  uint16
	maxItem  uint16
	keyExpr  string
}

func openReadOnlyNTX(filename string) (*readOnlyNTX, error) {
	f, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	ix := &readOnlyNTX{file: f}
	if err := ix.readHeader(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return ix, nil
}

func (ix *readOnlyNTX) Close() error {
	if ix == nil || ix.file == nil {
		return nil
	}
	return ix.file.Close()
}

func (ix *readOnlyNTX) readHeader() error {
	var raw [ntxPageSize]byte
	if _, err := ix.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.ReadFull(ix.file, raw[:]); err != nil {
		return fmt.Errorf("reading NTX header: %w", err)
	}

	sig := binary.LittleEndian.Uint16(raw[0:2])
	if sig != 0x0003 && sig != 0x0006 && sig != 0x0026 {
		return fmt.Errorf("unsupported NTX signature 0x%04X", sig)
	}

	ix.root = int64(binary.LittleEndian.Uint32(raw[4:8]))
	ix.itemSize = binary.LittleEndian.Uint16(raw[12:14])
	ix.keySize = binary.LittleEndian.Uint16(raw[14:16])
	ix.maxItem = binary.LittleEndian.Uint16(raw[18:20])

	expr := raw[22 : 22+256]
	if i := bytes.IndexByte(expr, 0); i >= 0 {
		expr = expr[:i]
	}
	ix.keyExpr = strings.TrimSpace(string(expr))

	if ix.keySize == 0 || ix.keySize > 250 {
		return fmt.Errorf("invalid NTX key size %d", ix.keySize)
	}
	if ix.itemSize != ix.keySize+8 {
		return fmt.Errorf("invalid NTX item size %d for key size %d", ix.itemSize, ix.keySize)
	}
	maxItem := uint16((ntxPageSize-2)/(ix.itemSize+2) - 1)
	if ix.maxItem == 0 || ix.maxItem > maxItem {
		return fmt.Errorf("invalid NTX max item count %d", ix.maxItem)
	}
	if ix.root == 0 || ix.root%ntxPageSize != 0 {
		return fmt.Errorf("invalid NTX root page offset %d", ix.root)
	}
	return nil
}

func (ix *readOnlyNTX) readNode(offset int64) (*ntxNode, error) {
	if offset <= 0 || offset%ntxPageSize != 0 {
		return nil, fmt.Errorf("invalid NTX page offset %d", offset)
	}

	var raw [ntxPageSize]byte
	if _, err := ix.file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(ix.file, raw[:]); err != nil {
		return nil, fmt.Errorf("reading NTX page at %d: %w", offset, err)
	}

	count := int(binary.LittleEndian.Uint16(raw[0:2]))
	if count > int(ix.maxItem) {
		return nil, fmt.Errorf("NTX page at %d claims %d items, max is %d", offset, count, ix.maxItem)
	}
	n := &ntxNode{items: make([]ntxItem, count), leaf: true}

	readItem := func(slot int) (int64, uint32, []byte, error) {
		slotOffset := 2 + 2*slot
		if slotOffset+2 > len(raw) {
			return 0, 0, nil, fmt.Errorf("NTX page at %d has invalid slot %d", offset, slot)
		}
		itemOffset := int(binary.LittleEndian.Uint16(raw[slotOffset : slotOffset+2]))
		if itemOffset < 2 || itemOffset+int(ix.itemSize) > len(raw) {
			return 0, 0, nil, fmt.Errorf("NTX page at %d slot %d points outside page", offset, slot)
		}
		child := int64(binary.LittleEndian.Uint32(raw[itemOffset : itemOffset+4]))
		recno := binary.LittleEndian.Uint32(raw[itemOffset+4 : itemOffset+8])
		key := append([]byte(nil), raw[itemOffset+8:itemOffset+8+int(ix.keySize)]...)
		return child, recno, key, nil
	}

	for i := 0; i < count; i++ {
		child, recno, key, err := readItem(i)
		if err != nil {
			return nil, err
		}
		n.items[i] = ntxItem{child: child, recno: recno, key: key}
		if child != 0 {
			n.leaf = false
		}
	}

	right, _, _, err := readItem(count)
	if err != nil {
		return nil, err
	}
	n.right = right
	if right != 0 {
		n.leaf = false
	}
	return n, nil
}

func (n *ntxNode) childAt(i int) int64 {
	if i == len(n.items) {
		return n.right
	}
	return n.items[i].child
}

func ntxCompare(aKey []byte, aRecno uint32, bKey []byte, bRecno uint32) int {
	if c := bytes.Compare(aKey, bKey); c != 0 {
		return c
	}
	if aRecno < bRecno {
		return -1
	}
	if aRecno > bRecno {
		return 1
	}
	return 0
}

func ntxFind(n *ntxNode, key []byte, recno uint32, strictlyAbove bool) int {
	lo, hi := 0, len(n.items)
	for lo < hi {
		mid := (lo + hi) / 2
		cmp := ntxCompare(n.items[mid].key, n.items[mid].recno, key, recno)
		if cmp < 0 || (strictlyAbove && cmp == 0) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (ix *readOnlyNTX) normalizeKey(key []byte) ([]byte, error) {
	if len(key) > int(ix.keySize) {
		return nil, fmt.Errorf("NTX key is %d bytes, index key size is %d", len(key), ix.keySize)
	}
	if len(key) == int(ix.keySize) {
		return key, nil
	}
	padded := make([]byte, ix.keySize)
	copy(padded, key)
	for i := len(key); i < len(padded); i++ {
		padded[i] = ' '
	}
	return padded, nil
}

func (ix *readOnlyNTX) findEntry(key []byte, recno uint32, strictlyAbove bool) (ntxEntry, bool, error) {
	var best ntxEntry
	found := false
	offset := ix.root
	for {
		n, err := ix.readNode(offset)
		if err != nil {
			return ntxEntry{}, false, err
		}
		i := ntxFind(n, key, recno, strictlyAbove)
		if i < len(n.items) {
			it := n.items[i]
			best = ntxEntry{key: it.key, recno: it.recno}
			found = true
		}
		if n.leaf {
			return best, found, nil
		}
		offset = n.childAt(i)
	}
}

func (ix *readOnlyNTX) exactRecordNumbers(key []byte) ([]uint32, error) {
	normalized, err := ix.normalizeKey(key)
	if err != nil {
		return nil, err
	}
	entry, found, err := ix.findEntry(normalized, 0, false)
	if err != nil {
		return nil, err
	}
	if !found || !bytes.Equal(entry.key, normalized) {
		return nil, nil
	}

	recnos := make([]uint32, 0, 1)
	for found && bytes.Equal(entry.key, normalized) {
		recnos = append(recnos, entry.recno)
		entry, found, err = ix.findEntry(entry.key, entry.recno, true)
		if err != nil {
			return nil, err
		}
	}
	return recnos, nil
}
