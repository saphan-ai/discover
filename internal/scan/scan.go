// Package scan runs the scanners over one repository and gathers their sections into a model.Repo.
//
// The registry is Scanners. A later wagon adds one line to it, and its scanner fills its own section of State.Repo.
// Scanners run in the order listed, one after the other: a scanner may rely on what an earlier one left in State
// (the structure scanner reads the file list the inventory scanner produced).
//
// Nothing here writes to the target, and every file a scanner reads goes through State.Manifest.
package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/manifest"
	"github.com/saphan-ai/discover/internal/model"
	"github.com/saphan-ai/discover/internal/scan/inventory"
	"github.com/saphan-ai/discover/internal/scan/structure"
)

// Options tune one run.
type Options struct {
	// MaxBytes is the size cap of one file read; zero means inventory.DefaultMaxBytes.
	MaxBytes int64
	// Attest carries the git settings.
	Attest attest.Options
}

// State is what a scanner reads and fills.
type State struct {
	// Root is the real, absolute path of the target.
	Root string
	Mode attest.Mode
	Opts Options
	// Manifest records every file read under Root.
	Manifest *manifest.Manifest
	// Files is every path the inventory listed, sorted. It is empty until the inventory scanner has run.
	Files []string
	// Skipped is every file a scanner did not read or did not count, with the reason.
	Skipped []inventory.Skipped
	// Repo is the report of the repository; each scanner fills its own section.
	Repo *model.Repo
}

// Scanner fills one section of the report.
type Scanner struct {
	Name string
	Run  func(*State) error
}

// Scanners is the registry, in the order they run.
var Scanners = []Scanner{
	{"inventory", runInventory},
	{"structure", runStructure},
}

func runInventory(s *State) error {
	res, err := inventory.Scan(s.Root, s.Mode, s.Manifest, inventory.Options{MaxBytes: s.Opts.MaxBytes, Attest: s.Opts.Attest})
	if err != nil {
		return err
	}
	s.Repo.Inventory = res.Inventory
	s.Files = res.Files
	s.Skipped = append(s.Skipped, res.Skipped...)
	return nil
}

func runStructure(s *State) error {
	s.Repo.Structure = structure.Scan(s.Files)
	return nil
}

// Result is what one run found. Repo holds only the sections the registered scanners fill; the rest is left for the
// caller (the attestation and the other sections).
type Result struct {
	Repo         model.Repo
	Skipped      []inventory.Skipped
	ReadManifest model.ReadManifest
}

// Run scans root, which is an existing directory, in the given mode.
func Run(root string, mode attest.Mode, opts Options) (Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Result{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", filepath.Base(abs), cause(err))
	}
	s := &State{Root: real, Mode: mode, Opts: opts, Manifest: manifest.New(real), Repo: &model.Repo{Name: filepath.Base(abs)}}
	for _, sc := range Scanners {
		if err := sc.Run(s); err != nil {
			return Result{}, fmt.Errorf("%s scanner: %s: %w", sc.Name, filepath.Base(abs), cause(err))
		}
	}
	rm := model.ReadManifest{Entries: []model.ManifestEntry{}}
	for _, e := range s.Manifest.Entries() {
		rm.Entries = append(rm.Entries, model.ManifestEntry{Path: e.Path, SHA256: e.SHA256, Bytes: int(e.Size)})
		rm.BytesRead += int(e.Size)
	}
	rm.FilesRead = len(rm.Entries)
	return Result{Repo: *s.Repo, Skipped: s.Skipped, ReadManifest: rm}, nil
}

// cause drops the path an operating system error carries, so an error never names the absolute root.
func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
