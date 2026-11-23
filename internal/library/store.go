// Package library maintains the offline index of local book files.
package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/heartleo/zlib/internal/ebookmeta"
	_ "modernc.org/sqlite"
)

// Book describes a file and the reader's annotations in the local bookshelf.
type Book struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Authors    string   `json:"authors"`
	Path       string   `json:"path"`
	Format     string   `json:"format"`
	Status     string   `json:"status"`
	Tags       []string `json:"tags"`
	Missing    bool     `json:"missing"`
	LastOpened string   `json:"last_opened,omitempty"`
}

type Store struct{ db *sql.DB }

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "zlib", "library.db"), nil
}

const schema = `
CREATE TABLE IF NOT EXISTS roots (prefix TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS books (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	path_key TEXT NOT NULL UNIQUE,
	path TEXT NOT NULL,
	title TEXT NOT NULL,
	authors TEXT NOT NULL,
	format TEXT NOT NULL,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('unread','reading','read')),
	tags TEXT NOT NULL DEFAULT '[]',
	missing INTEGER NOT NULL DEFAULT 0,
	last_opened TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS removed_paths (path_key TEXT PRIMARY KEY);`

func Open(dbPath string) (*Store, error) {
	path, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// An existing configuration directory may be shared. Keep the index private.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	// SQLite URI filenames take forward slashes, including before a drive letter.
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	dsn := url.URL{Scheme: "file", Path: name, RawQuery: "_pragma=busy_timeout(10000)&_txlock=immediate"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func initialize(db *sql.DB) error {
	const check = "SELECT EXISTS(SELECT 1 FROM pragma_table_info('books') WHERE name = 'last_opened')"
	var hasLastOpened bool
	if err := db.QueryRow(check).Scan(&hasLastOpened); err != nil || hasLastOpened {
		return err
	}
	// Begin an immediate transaction so two processes opening an older index
	// cannot both observe the missing column and attempt the same migration.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if err := tx.QueryRow(check).Scan(&hasLastOpened); err != nil {
		return err
	}
	if !hasLastOpened {
		if _, err := tx.Exec("ALTER TABLE books ADD COLUMN last_opened TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// cleanPath resolves links in the parent path, leaving the final component
// intact so that a link can be skipped and a renamed file can still be found.
func cleanPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty file path")
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path, nil
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func pathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func rootPrefix(key string) string {
	return strings.TrimSuffix(key, string(filepath.Separator)) + string(filepath.Separator)
}

const bookColumns = "id, title, authors, path, format, status, tags, missing, last_opened"

func readBook(row interface{ Scan(...any) error }) (Book, error) {
	var b Book
	var tags string
	err := row.Scan(&b.ID, &b.Title, &b.Authors, &b.Path, &b.Format, &b.Status, &tags, &b.Missing, &b.LastOpened)
	if err == nil {
		err = json.Unmarshal([]byte(tags), &b.Tags)
	}
	return b, err
}

func (s *Store) List(query string) ([]Book, error) {
	rows, err := s.db.Query("SELECT " + bookColumns + " FROM books ORDER BY last_opened DESC, title COLLATE NOCASE, path_key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	query = strings.ToLower(strings.TrimSpace(query))
	books := []Book{}
	for rows.Next() {
		b, err := readBook(rows)
		if err != nil {
			return nil, err
		}
		// Go case folding also handles characters outside SQLite's ASCII NOCASE.
		matches := func(s string) bool { return strings.Contains(strings.ToLower(s), query) }
		if query == "" || matches(b.Title) || matches(b.Authors) || matches(b.Path) ||
			matches(b.Format) || matches(b.Status) || slices.ContainsFunc(b.Tags, matches) {
			books = append(books, b)
		}
	}
	return books, rows.Err()
}

// MarkOpened records a successful preview or reader launch if the file
// belongs to the bookshelf. Fixed-width UTC timestamps sort chronologically.
func (s *Store) MarkOpened(name string) error {
	path, err := cleanPath(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
	_, err = s.db.Exec("UPDATE books SET last_opened = max(last_opened, ?) WHERE path_key = ?", stamp, pathKey(path))
	return err
}

func (s *Store) SetStatus(id int64, status string) error {
	switch status {
	case "unread", "reading", "read":
	default:
		return fmt.Errorf("unknown reading status %q (choose unread, reading or read)", status)
	}
	return changed(id, s.db.Exec, "UPDATE books SET status = ? WHERE id = ?", status, id)
}

func (s *Store) SetTags(id int64, tags []string) error {
	cleaned := make([]string, 0, len(tags))
	seen := make(map[string]bool)
	for _, tag := range tags {
		tag = ebookmeta.CleanLine(tag)
		key := strings.ToLower(tag)
		if tag != "" && !seen[key] {
			seen[key] = true
			cleaned = append(cleaned, tag)
		}
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return err
	}
	return changed(id, s.db.Exec, "UPDATE books SET tags = ? WHERE id = ?", string(data), id)
}

func changed(id int64, exec func(string, ...any) (sql.Result, error), query string, args ...any) error {
	result, err := exec(query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if count == 0 && err == nil {
		return fmt.Errorf("book %d: %w", id, sql.ErrNoRows)
	}
	return err
}

// Remove forgets the entry and its annotations. An automatic rescan should
// respect that decision; an explicit Add registers the file again.
func (s *Store) Remove(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR IGNORE INTO removed_paths SELECT path_key FROM books WHERE id = ?", id); err != nil {
		return err
	}
	if err := changed(id, tx.Exec, "DELETE FROM books WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}
