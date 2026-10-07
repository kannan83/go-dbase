package dbase

// indexedIO decorates an IO implementation with optional read-only NTX
// acceleration for exact searches. All operations other than Search are
// delegated unchanged to the wrapped implementation.
type indexedIO struct {
	IO
}

func (i indexedIO) OpenTable(config *Config) (*File, error) {
	file, err := i.IO.OpenTable(config)
	if err != nil {
		return nil, err
	}

	// Concrete OpenTable implementations set file.io to themselves. Restore the
	// decorator so File.Search continues to be a pure facade into the active IO
	// implementation.
	file.io = indexedIO{IO: file.io}
	return file, nil
}

func (i indexedIO) Search(file *File, field *Field, exactMatch bool) ([]*Row, error) {
	if exactMatch && field != nil {
		rows, used, err := file.indexedSearchExact(field)
		if err != nil {
			debugf("NTX search for field %s unavailable, falling back to DBF scan: %v", field.Name(), err)
		} else if used {
			debugf("NTX search used for field %s, matched %d row(s)", field.Name(), len(rows))
			return rows, nil
		}
	}

	return i.IO.Search(file, field, exactMatch)
}

func wrapIOWithIndexes(ioImpl IO, config *Config) IO {
	if ioImpl == nil || config == nil || len(config.Indexes) == 0 {
		return ioImpl
	}
	return indexedIO{IO: ioImpl}
}
