package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/heartleo/zlib/internal/ebookmeta"
	"github.com/heartleo/zlib/internal/library"
	"github.com/heartleo/zlib/internal/preview"
	"github.com/spf13/cobra"
)

// withLibrary opens the user's index for one operation. Background updates
// only touch a bookshelf the reader has already created.
func withLibrary(existingOnly bool, fn func(*library.Store) error) error {
	path, err := library.DefaultPath()
	if err != nil {
		return err
	}
	if existingOnly {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
	}
	store, err := library.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	return fn(store)
}

func libraryDownloadWarning(path string) string {
	err := withLibrary(true, func(s *library.Store) error { return s.RecordDownload(path) })
	if err != nil {
		return "File saved, but the bookshelf could not be updated: " + safeText(err.Error())
	}
	return ""
}

func updateLibraryPath(oldPath, newPath string) error {
	return withLibrary(true, func(s *library.Store) error { return s.Relocate(oldPath, newPath) })
}

func recordBookOpened(path string) error {
	return withLibrary(true, func(s *library.Store) error { return s.MarkOpened(path) })
}

func canPreviewBook(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".pdf" || ext == ".epub"
}

func openLibraryFile(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("choose a regular local book file")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !slices.Contains(ebookmeta.Extensions, ext) {
		return errors.New("the system reader can only open supported book formats")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	_, err = ebookmeta.Detect(file, info.Size())
	file.Close()
	if err != nil {
		return fmt.Errorf("could not recognize this file as a book: %w", err)
	}
	if err := launchBookReader(path); err != nil {
		return err
	}
	if err := recordBookOpened(path); err != nil {
		return fmt.Errorf("opened the book, but could not update its reading history: %w", err)
	}
	return nil
}

func libraryList(cmd *cobra.Command, query string) error {
	return withLibrary(false, func(s *library.Store) error {
		books, err := s.List(query)
		if err != nil {
			return err
		}
		jsonOut, _ := cmd.Flags().GetBool("json")
		if jsonOut {
			return printJSONTo(cmd.OutOrStdout(), books)
		}
		if len(books) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No matching books. Use zlib library add <file> or zlib library scan <directory>.")
		}
		for _, b := range books {
			status := b.Status
			if b.Missing {
				status += ", missing"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, bookCell(b.Title), bookCell(b.Authors), bookCell(b.Format), status, bookCell(strings.Join(b.Tags, ", ")), bookCell(b.Path))
		}
		return nil
	})
}

// libraryEdit supplies the same ID lookup and validation for scripted edits.
func libraryEdit(use, short string, args cobra.PositionalArgs, edit func(*library.Store, int64, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("expected a positive local book ID from zlib library list")
		}
		return withLibrary(false, func(s *library.Store) error { return edit(s, id, args[1:]) })
	}}
}

func init() {
	shelf := &cobra.Command{Use: "library", Short: "Browse and manage the local bookshelf", Args: cobra.NoArgs}
	shelf.RunE = func(cmd *cobra.Command, args []string) error {
		if !isInteractive() {
			return libraryList(cmd, "")
		}
		return runStandaloneShellCommand("/library")
	}
	list := &cobra.Command{Use: "list [query]", Short: "List or search registered books", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return libraryList(cmd, strings.Join(args, ""))
	}}
	list.Flags().Bool("json", false, "Print the books as JSON")
	add := &cobra.Command{Use: "add <file>", Short: "Register a book in place", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return withLibrary(false, func(s *library.Store) error {
			b, err := s.Add(args[0])
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\n", b.ID, bookCell(b.Title))
			}
			return err
		})
	}}
	scan := &cobra.Command{Use: "scan <directory>", Short: "Index a book directory and register it for future downloads", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return withLibrary(false, func(s *library.Store) error {
			count, err := s.Scan(args[0])
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Indexed %d book(s).\n", count)
			}
			return err
		})
	}}
	shelf.AddCommand(add, scan, list,
		libraryEdit("status <id> <unread|reading|read>", "Set reading status", cobra.ExactArgs(2), func(s *library.Store, id int64, args []string) error { return s.SetStatus(id, args[0]) }),
		libraryEdit("tags <id> [tag ...]", "Replace tags, or clear them by omitting tags", cobra.MinimumNArgs(1), func(s *library.Store, id int64, args []string) error { return s.SetTags(id, args) }),
		libraryEdit("remove <id>", "Remove an entry from the bookshelf and keep its file", cobra.ExactArgs(1), func(s *library.Store, id int64, _ []string) error { return s.Remove(id) }),
	)
	show := &cobra.Command{Use: "preview <file>", Short: "Preview a local PDF page or EPUB chapter", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isInteractive() && !cmd.Flags().Changed("page") {
			return runStandaloneShellCommand("/preview " + args[0])
		}
		n, _ := cmd.Flags().GetInt("page")
		if n < 1 {
			return errors.New("page or chapter number must be positive")
		}
		page, err := preview.Load(cmd.Context(), args[0], n-1, false)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), safeText(page.Text))
		if err == nil {
			if markErr := recordBookOpened(args[0]); markErr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "Could not update reading history:", safeText(markErr.Error()))
			}
		}
		return err
	}}
	show.Flags().Int("page", 1, "Print the text of a page or chapter (starting at 1)")
	rootCmd.AddCommand(shelf, show)
}
