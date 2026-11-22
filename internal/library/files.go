package library

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/heartleo/zlib/internal/ebookmeta"
)

func supported(path string) bool {
	return slices.Contains(ebookmeta.Extensions, strings.ToLower(filepath.Ext(path)))
}

func regularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !supported(path) {
		return nil, fmt.Errorf("%w: %s", ebookmeta.ErrUnsupportedFormat, filepath.Base(path))
	}
	return info, nil
}

type fileMetadata struct{ title, authors, format string }

func inspect(path string) (fileMetadata, error) {
	ext := filepath.Ext(path)
	m, format, err := ebookmeta.ReadFile(path)
	if format == "" {
		return fileMetadata{}, err
	}
	// A recognizable file may have damaged metadata, or an encrypted PDF may
	// only expose its header. Such books can still be found by filename.
	data := fileMetadata{format: string(format), title: ebookmeta.CleanLine(strings.TrimSuffix(filepath.Base(path), ext))}
	if data.title == "" {
		data.title = string(format)
	}
	if format == ebookmeta.FormatMOBI {
		switch suffix := strings.ToLower(ext); suffix {
		case ".azw", ".azw3":
			data.format = strings.TrimPrefix(suffix, ".")
		}
	}
	if m != nil && err == nil {
		if m.Title != "" {
			data.title = m.Title
		}
		data.authors = strings.Join(m.Authors, ", ")
	}
	return data, nil
}

// index updates file-derived fields only. Automatic imports respect removals
// and skip metadata reads when the file has the same size and modification time.
func index(tx *sql.Tx, path string, info os.FileInfo, explicit bool) (bool, error) {
	key := pathKey(path)
	if explicit {
		if _, err := tx.Exec("DELETE FROM removed_paths WHERE path_key = ?", key); err != nil {
			return false, err
		}
	} else {
		var excluded bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM removed_paths WHERE path_key = ?)", key).Scan(&excluded); err != nil || excluded {
			return false, err
		}
		result, err := tx.Exec("UPDATE books SET path = ?, missing = 0 WHERE path_key = ? AND size = ? AND mtime = ?",
			path, key, info.Size(), info.ModTime().UnixNano())
		if err != nil {
			return false, err
		}
		count, err := result.RowsAffected()
		if count > 0 || err != nil {
			return count > 0, err
		}
	}
	data, err := inspect(path)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO books (path_key, path, title, authors, format, size, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(path_key) DO UPDATE SET
		path = excluded.path, title = excluded.title, authors = excluded.authors,
		format = excluded.format, size = excluded.size, mtime = excluded.mtime, missing = 0`,
		key, path, data.title, data.authors, data.format, info.Size(), info.ModTime().UnixNano())
	return err == nil, err
}

func (s *Store) Add(name string) (Book, error) {
	path, err := cleanPath(name)
	if err != nil {
		return Book{}, err
	}
	info, err := regularFile(path)
	if err != nil {
		return Book{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Book{}, err
	}
	defer tx.Rollback()
	if _, err := index(tx, path, info, true); err != nil {
		return Book{}, err
	}
	b, err := readBook(tx.QueryRow("SELECT "+bookColumns+" FROM books WHERE path_key = ?", pathKey(path)))
	if err != nil {
		return Book{}, err
	}
	return b, tx.Commit()
}

// Scan registers a directory and reconciles its subtree in one transaction.
// A directory read failure cannot leave half of its books marked missing.
func (s *Store) Scan(dir string) (int, error) {
	root, err := cleanPath(dir)
	if err != nil {
		return 0, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("%s is not a directory", dir)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	prefix := rootPrefix(pathKey(root))
	if _, err := tx.Exec("INSERT OR IGNORE INTO roots (prefix) VALUES (?)", prefix); err != nil {
		return 0, err
	}
	if _, err := tx.Exec("UPDATE books SET missing = 1 WHERE substr(path_key, 1, length(?)) = ?", prefix, prefix); err != nil {
		return 0, err
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.Type().IsRegular() || !supported(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		included, err := index(tx, path, info, false)
		if errors.Is(err, ebookmeta.ErrUnsupportedFormat) || errors.Is(err, ebookmeta.ErrMalformed) {
			return nil
		}
		if included {
			count++
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// RecordDownload indexes the completed file only when it belongs to a root
// the reader has registered. Unrecognized downloads leave the catalog alone.
func (s *Store) RecordDownload(name string) error {
	if !supported(name) {
		return nil
	}
	path, err := cleanPath(name)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var registered bool
	// Store the separator boundary in the comparison: a sibling named
	// "books-old" is outside a root named "books". Root itself may be a volume.
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM roots WHERE substr(?, 1, length(prefix)) = prefix)",
		pathKey(path)).Scan(&registered)
	if err != nil || !registered {
		return err
	}
	info, err := regularFile(path)
	if err == nil {
		_, err = index(tx, path, info, false)
	}
	if errors.Is(err, ebookmeta.ErrUnsupportedFormat) || errors.Is(err, ebookmeta.ErrMalformed) {
		return nil
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Relocate updates an existing entry after a successful rename or metadata
// write. Its stable ID and reader annotations stay with the file.
func (s *Store) Relocate(oldName, newName string) error {
	oldPath, err := cleanPath(oldName)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow("SELECT id FROM books WHERE path_key = ?", pathKey(oldPath)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	path, err := cleanPath(newName)
	if err != nil {
		return err
	}
	info, err := regularFile(path)
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE books SET path_key = ?, path = ? WHERE id = ?", pathKey(path), path, id); err != nil {
		return err
	}
	if _, err := index(tx, path, info, true); err != nil {
		return err
	}
	return tx.Commit()
}
