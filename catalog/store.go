package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// ErrNoSnapshot is returned when no usable snapshot exists.
var ErrNoSnapshot = errors.New("no usable catalog snapshot")

// Store holds the current committed catalog version and serializes rebuilds.
// Queries are wait-free: they read the atomic version pointer and never block
// on a rebuild.
type Store struct {
	mu  sync.Mutex
	cur atomic.Pointer[Version]
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{} }

// Current returns the currently committed version (may be nil).
func (s *Store) Current() *Version { return s.cur.Load() }

// Publish atomically commits a new version.
func (s *Store) Publish(v *Version) { s.cur.Store(v) }

// Update performs a full build (when empty) or an incremental rebuild and
// atomically publishes the result. Rebuilds are serialized; queries keep
// running against the previously committed version.
func (s *Store) Update(ctx context.Context, cfg Config) (*Version, BuildReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.Current()
	var v *Version
	var report BuildReport
	var err error
	if prev == nil {
		v, report, err = Build(ctx, cfg)
	} else {
		v, report, err = Rebuild(ctx, prev, cfg)
	}
	if err != nil {
		return nil, report, err
	}
	s.Publish(v)
	return v, report, nil
}

// ---- crash-safe snapshots ----

const snapshotVersion = 1

type snapshotFile struct {
	Version     int                          `json:"version"`
	ID          int64                        `json:"id"`
	Config      Config                       `json:"config"`
	PseudoLocale string                      `json:"pseudo_locale"`
	Sources     map[string]SourceFile        `json:"sources"`
	FileEntries map[string]map[string]string `json:"file_entries"`
}

// SaveSnapshot atomically persists the version to dir. A crash at any point
// leaves either the previous or the new snapshot usable, never a partial one.
func (s *Store) SaveSnapshot(dir string) error {
	v := s.Current()
	if v == nil {
		return fmt.Errorf("nothing to save")
	}
	snap := snapshotFile{
		Version:      snapshotVersion,
		ID:           v.ID,
		Config:       v.Config,
		PseudoLocale: v.PseudoLocale,
		Sources:      v.Sources,
		FileEntries:  map[string]map[string]string{},
	}
	for path, entries := range v.FileEntries {
		raws := map[string]string{}
		for key, e := range entries {
			raws[key] = e.Raw
		}
		snap.FileEntries[path] = raws
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	sha := []byte(hex.EncodeToString(sum[:]) + "\n")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "snapshot.json.tmp")
	tmpSha := filepath.Join(dir, "snapshot.sha256.tmp")
	if err := writeFsync(tmp, data); err != nil {
		return err
	}
	if err := writeFsync(tmpSha, sha); err != nil {
		return err
	}
	cur := filepath.Join(dir, "snapshot.json")
	curSha := filepath.Join(dir, "snapshot.sha256")
	prev := filepath.Join(dir, "snapshot.prev.json")
	prevSha := filepath.Join(dir, "snapshot.prev.sha256")

	// Shift current -> prev, then tmp -> current. Each rename is atomic; a
	// crash between them simply falls back to the previous snapshot.
	if exists(cur) {
		if err := os.Rename(cur, prev); err != nil {
			return err
		}
	}
	if exists(curSha) {
		if err := os.Rename(curSha, prevSha); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, cur); err != nil {
		return err
	}
	if err := os.Rename(tmpSha, curSha); err != nil {
		return err
	}
	// Keep a previous snapshot even on the first save so a later corruption
	// can always fall back to a complete earlier version.
	if !exists(prev) {
		if err := copyFile(cur, prev); err != nil {
			return err
		}
		if err := copyFile(curSha, prevSha); err != nil {
			return err
		}
	}
	// Best-effort directory fsync.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// LoadSnapshot restores a version previously saved with SaveSnapshot. It
// verifies the checksum and falls back to the previous snapshot if needed.
func (s *Store) LoadSnapshot(dir string) (*Version, BuildReport, error) {
	snap, err := readSnapshot(dir, "snapshot.json", "snapshot.sha256")
	if err != nil {
		snap, err = readSnapshot(dir, "snapshot.prev.json", "snapshot.prev.sha256")
		if err != nil {
			return nil, BuildReport{}, fmt.Errorf("%w: %v", ErrNoSnapshot, err)
		}
	}
	v, report, err := restoreSnapshot(snap)
	if err != nil {
		return nil, BuildReport{}, err
	}
	s.Publish(v)
	return v, report, nil
}

func readSnapshot(dir, name, shaName string) (*snapshotFile, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	shaBytes, err := os.ReadFile(filepath.Join(dir, shaName))
	if err != nil {
		return nil, fmt.Errorf("missing checksum file")
	}
	got := sha256.Sum256(data)
	want, err := hex.DecodeString(string(trimSpace(shaBytes)))
	if err != nil || !equalBytes(got[:], want) {
		return nil, fmt.Errorf("checksum mismatch")
	}
	var snap snapshotFile
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", snap.Version)
	}
	return &snap, nil
}

func restoreSnapshot(snap *snapshotFile) (*Version, BuildReport, error) {
	// Re-parse every entry so ASTs and validation errors are fresh.
	fileEntries := map[string]map[string]*Entry{}
	parsed := map[string]map[string]*Entry{}
	for path, raws := range snap.FileEntries {
		entries := map[string]*Entry{}
		for key, raw := range raws {
			entries[key] = &Entry{Key: key, Raw: raw}
		}
		fileEntries[path] = entries
		parsed[path] = entries
	}
	locales, _ := mergeLocales(sourceFilesOf(snap.Sources), parsed)
	for _, lc := range locales {
		for key, e := range lc.Entries {
			_, errs := ValidateEntry(lc.Locale, key, e.Raw)
			_ = errs
		}
	}
	errs := validateAll(locales)
	resolved, missErrs := resolveAll(locales, snap.Config)
	errs = append(errs, missErrs...)
	pseudoLocale, pseudoEntries := buildPseudo(locales, snap.Config)
	if pseudoLocale != "" {
		locales[pseudoLocale] = &LocaleCatalog{Locale: pseudoLocale, Entries: pseudoEntries}
		if resolved[pseudoLocale] == nil {
			resolved[pseudoLocale] = map[string]*Resolution{}
		}
		for key, e := range pseudoEntries {
			resolved[pseudoLocale][key] = &Resolution{
				Locale: pseudoLocale, Key: key, SourceLocale: pseudoLocale, Entry: e,
				Chain: []ChainStep{{Locale: pseudoLocale, Hit: true, Reason: "hit"}},
			}
		}
	}
	v := &Version{
		ID:           snap.ID,
		Config:       snap.Config,
		Locales:      locales,
		Resolved:     resolved,
		Sources:      snap.Sources,
		FileEntries:  fileEntries,
		PseudoLocale: pseudoLocale,
	}
	return v, BuildReport{VersionID: v.ID, FullRebuild: true, Errors: errs}, nil
}

func sourceFilesOf(sources map[string]SourceFile) []scannedFile {
	var out []scannedFile
	for path, sf := range sources {
		out = append(out, scannedFile{sf})
		_ = path
	}
	return out
}

func writeFsync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFsync(dst, data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
