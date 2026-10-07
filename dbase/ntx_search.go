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

	ix, err := openReadOnlyNTX(indexPath)
	if err != nil {
		return nil, false, err
	}
	defer ix.Close()

	// Read-only NTX support intentionally handles simple column expressions
	// only. The external application owns index creation and maintenance.
	if !strings.EqualFold(strings.TrimSpace(ix.keyExpr), strings.TrimSpace(field.Name())) {
		return nil, false, fmt.Errorf("NTX key expression %q does not match DBF field %q", ix.keyExpr, field.Name())
	}

	searchKey, err := file.Represent(field, false)
	if err != nil {
		return nil, false, err
	}
	recnos, err := ix.exactRecordNumbers(searchKey)
	if err != nil {
		return nil, false, err
	}

	// DBF and NTX are updated by another process and not atomically from our
	// point of view. A miss may therefore be transient, so use the old scan as
	// the correctness fallback rather than returning a false negative.
	if len(recnos) == 0 {
		return nil, false, fmt.Errorf("NTX lookup returned no matching record")
	}

	wanted, err := ix.normalizeKey(searchKey)
	if err != nil {
		return nil, false, err
	}
	rows := make([]*Row, 0, len(recnos))

	for _, recno := range recnos {
		if recno == 0 || recno > file.header.RowsCount {
			return nil, false, fmt.Errorf("NTX record number %d is outside DBF row range 1..%d", recno, file.header.RowsCount)
		}

		// NTX record numbers are one-based; go-dbase's row pointer is zero-based.
		if err := file.GoTo(recno - 1); err != nil {
			return nil, false, err
		}
		row, err := file.Row()
		if err != nil {
			return nil, false, err
		}
		actualField := row.FieldByName(field.Name())
		if actualField == nil {
			return nil, false, fmt.Errorf("field %s not found while verifying NTX result", field.Name())
		}
		actualKey, err := file.Represent(actualField, false)
		if err != nil {
			return nil, false, err
		}
		actualKey, err = ix.normalizeKey(actualKey)
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(actualKey, wanted) {
			return nil, false, fmt.Errorf("NTX record %d no longer matches %s", recno, field.Name())
		}
		rows = append(rows, row)
	}
	return rows, true, nil
}
