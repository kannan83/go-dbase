package dbase

import (
	"bytes"
	"fmt"
	"strings"
)

func (file *File) indexPathForField(fieldName string) (string, bool) {
	if file == nil || file.config == nil || len(file.config.Indexes) == 0 {
		return "", false
	}
	for name, filename := range file.config.Indexes {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(fieldName)) {
			filename = strings.TrimSpace(filename)
			return filename, filename != ""
		}
	}
	return "", false
}

// indexedSearchExact performs a read-only NTX lookup. The bool reports whether
// the index result is safe to return. On any NTX error or inconsistency Search
// falls back to the existing sequential DBF scan.
func (file *File) indexedSearchExact(field *Field) ([]*Row, bool, error) {
	if field == nil || field.column == nil {
		return nil, false, nil
	}
	indexPath, configured := file.indexPathForField(field.Name())
	if !configured {
		return nil, false, nil
	}

	debugf("Using NTX index %s for exact search on field %s", indexPath, field.Name())

	ix, err := openReadOnlyNTX(indexPath)
	if err != nil {
		return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
	}
	defer ix.Close()

	// Read-only NTX support intentionally handles simple column expressions
	// only. The external application owns index creation and maintenance.
	if !strings.EqualFold(strings.TrimSpace(ix.keyExpr), strings.TrimSpace(field.Name())) {
		return nil, false, fmt.Errorf("NTX index %s key expression %q does not match DBF field %q", indexPath, ix.keyExpr, field.Name())
	}

	searchKey, err := file.Represent(field, false)
	if err != nil {
		return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
	}
	recnos, err := ix.exactRecordNumbers(searchKey)
	if err != nil {
		return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
	}

	// A clean NTX miss is a valid indexed-search result, not an anomaly.
	// Only detected NTX/DBF inconsistencies should fall back to a full DBF scan.
	if len(recnos) == 0 {
		return []*Row{}, true, nil
	}

	wanted, err := ix.normalizeKey(searchKey)
	if err != nil {
		return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
	}
	rows := make([]*Row, 0, len(recnos))

	for _, recno := range recnos {
		if recno == 0 || recno > file.header.RowsCount {
			return nil, false, fmt.Errorf("NTX index %s record number %d is outside DBF row range 1..%d", indexPath, recno, file.header.RowsCount)
		}

		// NTX record numbers are one-based; go-dbase's row pointer is zero-based.
		if err := file.GoTo(recno - 1); err != nil {
			return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
		}
		row, err := file.Row()
		if err != nil {
			return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
		}
		actualField := row.FieldByName(field.Name())
		if actualField == nil {
			return nil, false, fmt.Errorf("NTX index %s: field %s not found while verifying result", indexPath, field.Name())
		}
		actualKey, err := file.Represent(actualField, false)
		if err != nil {
			return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
		}
		actualKey, err = ix.normalizeKey(actualKey)
		if err != nil {
			return nil, false, fmt.Errorf("NTX index %s: %w", indexPath, err)
		}
		if !bytes.Equal(actualKey, wanted) {
			return nil, false, fmt.Errorf("NTX index %s record %d no longer matches %s", indexPath, recno, field.Name())
		}
		rows = append(rows, row)
	}
	return rows, true, nil
}
