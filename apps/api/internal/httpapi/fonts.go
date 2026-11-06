package httpapi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"docibis/internal/merge"
	"docibis/internal/store"
)

// Fonts added by an administrator (brand fonts, licensed fonts) are kept in
// the database and installed by every task in a folder of its own, which
// fontconfig (FONTCONFIG_FILE, for LibreOffice) and the native engine
// (DOCIBIS_FONTS_ADDED) read; the workers are then replaced, since a
// process reads the fonts when it starts.

const (
	maxFontMB = 32
	// fontsAddedEnv names the folder of the fonts added, for native.py.
	fontsAddedEnv = "DOCIBIS_FONTS_ADDED"
	// recheckDelay lets an administrator add several fonts before every
	// template is compared again with its reference.
	recheckDelay = time.Minute
)

// fontState is what this task installed of the fonts added.
type fontState struct {
	mu sync.Mutex
	// dir holds fonts.conf, the fonts in files/ and the cache of fontconfig.
	dir string
	// applied sums the fonts installed.
	applied string
	recheck *time.Timer
}

var errNotFont = errors.New("not a TrueType or OpenType font")

// fontFace is the names of one font of a file.
type fontFace struct {
	// families are its typographic family (name 16) and its family (name
	// 1), as fontconfig lists them.
	families []string
	style    string
}

// readFontFaces reads the names of the font of a TrueType or OpenType file,
// or of each font of a collection, reading only their tables of names.
func readFontFaces(r io.ReaderAt, size int64) ([]fontFace, error) {
	be := binary.BigEndian
	head := make([]byte, 12)
	if _, err := r.ReadAt(head, 0); err != nil {
		return nil, errNotFont
	}
	offsets := []int64{0}
	if string(head[:4]) == "ttcf" {
		n := int64(be.Uint32(head[8:]))
		if n == 0 || n > 256 {
			return nil, errNotFont
		}
		buf := make([]byte, 4*n)
		if _, err := r.ReadAt(buf, 12); err != nil {
			return nil, errNotFont
		}
		offsets = offsets[:0]
		for i := range n {
			offsets = append(offsets, int64(be.Uint32(buf[4*i:])))
		}
	}
	faces := make([]fontFace, 0, len(offsets))
	for _, at := range offsets {
		face, err := readFontFace(r, size, at)
		if err != nil {
			return nil, err
		}
		faces = append(faces, face)
	}
	return faces, nil
}

func readFontFace(r io.ReaderAt, size, at int64) (fontFace, error) {
	be := binary.BigEndian
	head := make([]byte, 12)
	if _, err := r.ReadAt(head, at); err != nil {
		return fontFace{}, errNotFont
	}
	switch string(head[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true":
	default:
		return fontFace{}, errNotFont
	}
	n := int64(be.Uint16(head[4:]))
	dir := make([]byte, 16*n)
	if _, err := r.ReadAt(dir, at+12); err != nil {
		return fontFace{}, errNotFont
	}
	for i := range n {
		rec := dir[16*i:]
		if string(rec[:4]) != "name" {
			continue
		}
		off, length := int64(be.Uint32(rec[8:])), int64(be.Uint32(rec[12:]))
		if length < 6 || length > 1<<20 || off+length > size {
			return fontFace{}, errNotFont
		}
		table := make([]byte, length)
		if _, err := r.ReadAt(table, off); err != nil {
			return fontFace{}, errNotFont
		}
		return fontNames(table)
	}
	return fontFace{}, errNotFont
}

// fontNames reads a table of names, in English when it has it.
func fontNames(t []byte) (fontFace, error) {
	be := binary.BigEndian
	count, base := int(be.Uint16(t[2:])), int(be.Uint16(t[4:]))
	best, rank := map[uint16]string{}, map[uint16]int{}
	for i := range count {
		r := 6 + 12*i
		if r+12 > len(t) {
			break
		}
		platform, encoding, language, id := be.Uint16(t[r:]), be.Uint16(t[r+2:]), be.Uint16(t[r+4:]), be.Uint16(t[r+6:])
		if id != 1 && id != 2 && id != 16 && id != 17 {
			continue
		}
		start := base + int(be.Uint16(t[r+10:]))
		end := start + int(be.Uint16(t[r+8:]))
		if end > len(t) {
			continue
		}
		raw := t[start:end]
		var text string
		var score int
		switch {
		case platform == 3 && (encoding == 0 || encoding == 1 || encoding == 10):
			text, score = utf16BE(raw), 2
			if language == 0x409 {
				score = 3
			}
		case platform == 0:
			text, score = utf16BE(raw), 1
		case platform == 1 && encoding == 0:
			text, score = latin1(raw), 1
		default:
			continue
		}
		if text = strings.TrimSpace(text); text != "" && score > rank[id] {
			best[id], rank[id] = text, score
		}
	}
	face := fontFace{style: cmp.Or(best[17], best[2])}
	for _, f := range []string{best[16], best[1]} {
		if f != "" && !slices.Contains(face.families, f) {
			face.families = append(face.families, f)
		}
	}
	if len(face.families) == 0 {
		return face, errNotFont
	}
	return face, nil
}

func utf16BE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// latin1 reads the Mac Roman names of old fonts, the same in ASCII.
func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// fontExtension names a font file after its kind.
func fontExtension(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("ttcf")):
		return ".ttc"
	case bytes.HasPrefix(raw, []byte("OTTO")):
		return ".otf"
	}
	return ".ttf"
}

// newFont checks a font sent by an administrator.
func newFont(name string, raw []byte) (store.Font, error) {
	if len(raw) == 0 {
		return store.Font{}, refuse("Choisissez un fichier de police (.ttf, .otf ou .ttc).")
	}
	faces, err := readFontFaces(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return store.Font{}, refuse("Ce fichier n'est pas une police TrueType ou OpenType (.ttf, .otf ou .ttc).")
	}
	var styles []string
	for _, f := range faces {
		if f.style != "" && !slices.Contains(styles, f.style) {
			styles = append(styles, f.style)
		}
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(filepath.Base(name)))
	if name == "" || name == "." || len([]rune(name)) > 200 {
		name = faces[0].families[0] + fontExtension(raw)
	}
	sum := sha256.Sum256(raw)
	return store.Font{File: name, Family: faces[0].families[0], Style: strings.Join(styles, ", "), Size: int64(len(raw)),
		SHA256: hex.EncodeToString(sum[:]), Content: raw}, nil
}

// installed is the font families LibreOffice finds here, by lower-case
// name; read again after the fonts change.
var installed struct {
	sync.Mutex
	families map[string]string
	read     bool
}

// installedFonts lists the font families LibreOffice can use here, by
// lower-case name.
func installedFonts() map[string]string {
	installed.Lock()
	defer installed.Unlock()
	if !installed.read {
		installed.families, installed.read = scanFonts(fontDirs()), true
	}
	return installed.families
}

func forgetInstalledFonts() {
	installed.Lock()
	installed.read = false
	installed.Unlock()
}

// fontDirs are the folders LibreOffice takes its fonts from: those of the
// system, its own and the fonts added.
func fontDirs() []string {
	program := cmp.Or(os.Getenv("LO_PATH"), "/opt/libreoffice26.8/program")
	dirs := []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(filepath.Dir(program), "share", "fonts")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".fonts"), filepath.Join(home, ".local", "share", "fonts"))
	}
	if added := os.Getenv(fontsAddedEnv); added != "" {
		dirs = append(dirs, added)
	}
	return dirs
}

// scanFonts reads the families of the TrueType and OpenType fonts of dirs.
func scanFonts(dirs []string) map[string]string {
	fonts := map[string]string{}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ttf", ".otf", ".ttc":
			default:
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return nil
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return nil
			}
			faces, _ := readFontFaces(f, info.Size())
			for _, face := range faces {
				for _, family := range face.families {
					fonts[strings.ToLower(family)] = family
				}
			}
			return nil
		})
	}
	return fonts
}

// fontsSum sums the hashes of the fonts added.
func fontsSum(sums []string) string {
	h := sha256.New()
	for _, s := range sums {
		h.Write([]byte(s + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SyncFonts installs the fonts added: before the workers start, then when
// they change. It tells whether they did; the workers are then replaced.
func (s *Server) SyncFonts(ctx context.Context) (bool, error) {
	st := s.db.Load()
	if st == nil {
		return false, nil
	}
	sums, err := st.FontSums(ctx)
	if err != nil {
		return false, err
	}
	sum := fontsSum(sums)
	s.fonts.mu.Lock()
	defer s.fonts.mu.Unlock()
	if s.fonts.dir != "" && sum == s.fonts.applied {
		return false, nil
	}
	if s.fonts.dir == "" {
		dir, err := fontFolder()
		if err != nil {
			return false, err
		}
		s.fonts.dir = dir
	}
	fonts, err := st.FontFiles(ctx)
	if err != nil {
		return false, err
	}
	if err := writeFonts(filepath.Join(s.fonts.dir, "files"), fonts); err != nil {
		return false, err
	}
	s.fonts.applied = sum
	forgetInstalledFonts()
	s.recycleWorkers()
	return true, nil
}

// fontFolder makes the folder of the fonts added and points fontconfig and
// the native engine at it, for the workers started from now on.
func fontFolder() (string, error) {
	dir, err := os.MkdirTemp("", "docibis-fonts-")
	if err != nil {
		return "", err
	}
	files := filepath.Join(dir, "files")
	if err := os.Mkdir(files, 0o700); err != nil {
		return "", err
	}
	base := cmp.Or(os.Getenv("FONTCONFIG_FILE"), "/etc/fonts/fonts.conf")
	conf := fmt.Sprintf(`<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <include ignore_missing="yes">%s</include>
  <dir>%s</dir>
  <cachedir>%s</cachedir>
</fontconfig>
`, html.EscapeString(base), html.EscapeString(files), html.EscapeString(filepath.Join(dir, "cache")))
	path := filepath.Join(dir, "fonts.conf")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return "", err
	}
	if err := os.Setenv("FONTCONFIG_FILE", path); err != nil {
		return "", err
	}
	return dir, os.Setenv(fontsAddedEnv, files)
}

// writeFonts makes dir hold the fonts, each named after its hash, and
// nothing else.
func writeFonts(dir string, fonts []store.Font) error {
	keep := map[string]bool{}
	for _, f := range fonts {
		name := f.SHA256[:min(32, len(f.SHA256))] + fontExtension(f.Content)
		keep[name] = true
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p+".part", f.Content, 0o600); err != nil {
			return err
		}
		if err := os.Rename(p+".part", p); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// recycleWorkers replaces the workers of both engines: new processes read
// the fonts again.
func (s *Server) recycleWorkers() {
	type recycler interface{ Recycle() }
	if b := s.renderer.Load(); b != nil {
		if r, ok := b.Renderer.(recycler); ok {
			r.Recycle()
		}
	}
	if b := s.native.Load(); b != nil {
		if r, ok := b.NativeCompiler.(recycler); ok {
			r.Recycle()
		}
	}
}

// WatchFonts installs the fonts another task added or removed.
func (s *Server) WatchFonts(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if changed, err := s.SyncFonts(ctx); err != nil {
				s.logger.Warn("fonts not installed", "error", err)
			} else if changed {
				s.logger.Info("fonts installed: workers replaced")
			}
		}
	}
}

// fontsChanged installs the fonts here at once, has every Word template
// checked and qualified again with them, then compared with its reference.
func (s *Server) fontsChanged(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	if _, err := s.SyncFonts(ctx); err != nil {
		s.logger.Error("fonts not installed", "error", err)
	}
	if err := s.db.Load().ResetLint(ctx); err != nil {
		s.logger.Warn("templates not checked again", "error", err)
		return
	}
	s.describeLater(ctx)
	s.fonts.mu.Lock()
	defer s.fonts.mu.Unlock()
	if s.fonts.recheck != nil {
		s.fonts.recheck.Reset(recheckDelay)
		return
	}
	s.fonts.recheck = time.AfterFunc(recheckDelay, func() {
		if n, err := s.checkAll(ctx); err != nil {
			s.logger.Warn("reference check not started", "error", err)
		} else if n > 0 {
			s.logger.Info("fonts changed: comparing the templates with their references", "templates", n)
		}
	})
}

func addedFont(f store.Font) AddedFont {
	return AddedFont{Id: f.ID, File: f.File, Family: f.Family, Style: f.Style, Size: f.Size, CreatedAt: f.CreatedAt, CreatedBy: f.CreatedBy}
}

func (s *Server) ListFonts(ctx context.Context, _ ListFontsRequestObject) (ListFontsResponseObject, error) {
	st := s.db.Load()
	added, err := st.Fonts(ctx)
	if err != nil {
		return nil, err
	}
	out := FontsOverview{Added: make([]AddedFont, 0, len(added)), Installed: []string{}, Missing: []MissingFont{}}
	for _, f := range added {
		out.Added = append(out.Added, addedFont(f))
	}
	families := installedFonts()
	for _, name := range families {
		out.Installed = append(out.Installed, name)
	}
	slices.SortFunc(out.Installed, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })
	if len(families) > 0 {
		lints, err := st.PublishedLint(ctx)
		if err != nil {
			return nil, err
		}
		out.Missing = missingFonts(lints, fontInstalled(families))
	}
	return ListFonts200JSONResponse(out), nil
}

var fontIssue = regexp.MustCompile(`^Police « (.+?) »`)

// missingFonts lists the fonts the published templates lack, from their
// checks, as the server is now: the most used first.
func missingFonts(lints map[string]string, installed func(string) bool) []MissingFont {
	byFamily := map[string]*MissingFont{}
	for path, raw := range lints {
		var issues []merge.Issue
		if json.Unmarshal([]byte(raw), &issues) != nil {
			continue
		}
		for _, i := range issues {
			m := fontIssue.FindStringSubmatch(i.Message)
			if m == nil || (i.Code != "font-missing" && i.Code != "font-substituted") {
				continue
			}
			missing, sub := merge.FontCheck(m[1], installed)
			if !missing {
				continue
			}
			f := byFamily[m[1]]
			if f == nil {
				f = &MissingFont{Family: m[1], Templates: []string{}}
				if sub != "" {
					f.Substitute = &sub
				}
				byFamily[m[1]] = f
			}
			if !slices.Contains(f.Templates, path) {
				f.Templates = append(f.Templates, path)
			}
		}
	}
	out := make([]MissingFont, 0, len(byFamily))
	for _, f := range byFamily {
		slices.Sort(f.Templates)
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b MissingFont) int {
		return cmp.Or(cmp.Compare(len(b.Templates), len(a.Templates)), cmp.Compare(a.Family, b.Family))
	})
	return out
}

func (s *Server) AddFont(ctx context.Context, req AddFontRequestObject) (AddFontResponseObject, error) {
	f, err := readLibraryForm(req.Body, map[string]int{"file": maxFontMB})
	var font store.Font
	if err == nil {
		font, err = newFont(f.names["file"], f.files["file"])
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return AddFont413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return AddFont400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	font.CreatedBy = me.Username
	added, err := s.db.Load().AddFont(ctx, &font)
	if err != nil {
		return nil, err
	}
	if added {
		s.audit(ctx, me.Username, "font.add", font.File, strings.TrimSpace(font.Family+" "+font.Style))
		s.fontsChanged(ctx)
	}
	return AddFont201JSONResponse(addedFont(font)), nil
}

func (s *Server) DeleteFont(ctx context.Context, req DeleteFontRequestObject) (DeleteFontResponseObject, error) {
	font, ok, err := s.db.Load().DeleteFont(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteFont404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Police inconnue."))}, nil
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "font.delete", font.File, strings.TrimSpace(font.Family+" "+font.Style))
	s.fontsChanged(ctx)
	return DeleteFont204Response{}, nil
}
