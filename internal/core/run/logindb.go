package run

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/hostsandbox"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sqliteread"
)

// The credential table opencode keeps its logins in, and its columns: one
// row per login, the integration (provider) it signs in to, its value as a
// JSON object, whether `opencode auth switch` made it the integration's
// active one (NULL for a login it imported), and when it last changed —
// verified empirically against the agent's CLI.
const (
	loginTable      = "credential"
	loginColID      = "integration_id"
	loginColValue   = "value"
	loginColActive  = "active"
	loginColUpdated = "time_updated"
)

// mcpLoginPrefix starts the integration of an MCP server's login, which the
// table keeps beside the providers' and auth.json never carried.
const mcpLoginPrefix = "mcp_"

// The login types auth.json holds; the table's API key type ("key") reads
// as auth.json's "api".
const (
	loginTypeKey = "key"
	loginTypeAPI = "api"
)

// loginTypes are the auth.json login types a fresh database imports.
var loginTypes = []string{loginTypeAPI, "oauth", "wellknown"}

// loginDBAttempts bounds the reads of a database its CLI may be rewriting
// meanwhile: a read that meets a torn page or a reset log is tried again.
const loginDBAttempts = 3

// seedLoginDB writes the provider logins of the row name's database (c)
// into the throwaway home dir, over the seeded copy of c.Into, and returns the
// environment entry that points c.Env at the copy's database. The real
// database is the one the CLI opens: c.Env's value — absolute, or relative to
// c.Path's directory below source, as opencode resolves it — else c.Path.
// The file and the directories below its root handle are opened without
// following a link (the directories above an absolute c.Env value are the
// operator's own path, followed as written); one that is a link or not a
// regular file refuses the launch, while one that is missing is left out and
// one that cannot be read as a credential table is left out with a warning,
// so the copied login file is all the launch gets. A database beside it the
// CLI names by its release channel is one the seeder cannot predict: it
// warns rather than seed from a stale file silently.
func seedLoginDB(name string, c *agentcatalog.LoginDB, source, dir string, env []string,
) (entry, warning string, err error) {
	entry = c.Env + inlineValue + filepath.Join(dir, filepath.FromSlash(c.Path))
	dbDir, rel, ok := loginDBPath(c, source, hostsandbox.EnvLookup(env, c.Env))
	if !ok {
		return entry, "", nil
	}
	logins, why, err := readLogins(dbDir, rel)
	switch {
	case err != nil:
		return "", "", jq.NewErrf("the database %s: %v", filepath.Join(dbDir, rel), err)
	case why != "":
		return entry, name + "'s logins in " + filepath.Join(dbDir, rel) + " could not be read (" + why +
			"), so its review launch has only the copy of " + filepath.Base(c.Into), nil
	}
	warning = channelWarning(name, dbDir, rel)
	if len(logins) == 0 {
		return entry, warning, nil
	}
	return entry, warning, writeLogins(filepath.Join(dir, filepath.FromSlash(c.Into)), logins)
}

// loginDBPath is the directory the real database sits in and its name
// there; false when value keeps it in memory.
func loginDBPath(c *agentcatalog.LoginDB, source, value string) (dir, name string, ok bool) {
	def := filepath.Join(source, filepath.FromSlash(c.Path))
	switch {
	case value == memoryDB:
		return "", "", false
	case filepath.IsAbs(value):
		def = filepath.Clean(value)
	case value != "":
		def = filepath.Join(filepath.Dir(def), value)
	}
	return filepath.Dir(def), filepath.Base(def), true
}

// memoryDB is the database name that keeps it in memory, no file.
const memoryDB = ":memory:"

// channelWarning names the databases beside the default one that a build on
// another release channel opens instead (<stem>-<channel>.db), "" for none.
func channelWarning(name, dir, file string) string {
	stem, ext, ok := strings.Cut(file, ".")
	entries, err := os.ReadDir(dir)
	if !ok || err != nil {
		return ""
	}
	var others []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, stem+"-") && strings.HasSuffix(n, "."+ext) {
			others = append(others, n)
		}
	}
	if len(others) == 0 {
		return ""
	}
	return name + " keeps " + strings.Join(others, ", ") + " beside " + file + " in " + dir + ": a build on " +
		"another release channel signs in from that database, which verdict does not read, so its review " +
		"launch has " + file + "'s logins"
}

// loginFiles is one open login database: the root handle on its directory,
// the database, and its write-ahead log (nil when there is none).
type loginFiles struct {
	root *os.Root
	db   *os.File
	wal  *os.File
}

// Close closes what is open.
func (l *loginFiles) Close() {
	for _, f := range []*os.File{l.wal, l.db} {
		if f != nil {
			_ = f.Close()
		}
	}
	_ = l.root.Close()
}

// readLogins reads the provider logins of the database rel below dir: an
// error for a link or a file that is not regular, why for one that is not a
// readable credential table, and nothing for a missing one.
func readLogins(dir, rel string) (map[string]json.RawMessage, string, error) {
	files, err := openLoginDB(dir, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer files.Close()
	var why error
	for range loginDBAttempts {
		var rows []map[string]any
		if rows, why = readTable(files.db, files.wal); why == nil {
			return logins(rows), "", nil
		}
	}
	return nil, why.Error(), nil
}

// openLoginDB opens the database rel below dir and its log without
// following a link; [fs.ErrNotExist] when the database or a directory on
// its way is missing.
func openLoginDB(dir, rel string) (*loginFiles, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, jq.Sealed("open "+dir, err)
	}
	files := &loginFiles{root: root}
	if err := files.open(rel); err != nil {
		files.Close()
		return nil, err
	}
	return files, nil
}

// open opens the database rel and its log below the root handle.
func (l *loginFiles) open(rel string) error {
	present, err := dirsOnTheWay(l.root, filepath.Dir(rel))
	switch {
	case err != nil:
		return err
	case !present:
		return fs.ErrNotExist
	}
	if l.db, err = openRegular(l.root, rel); err != nil {
		return err
	}
	l.wal, err = openRegular(l.root, rel+"-wal")
	if errors.Is(err, fs.ErrNotExist) {
		l.wal, err = nil, nil
	}
	return err
}

// openRegular opens one file below root without following a link; an
// error wrapping [fs.ErrNotExist] for a missing one, and an error for one that
// is not a regular file.
func openRegular(root *os.Root, rel string) (*os.File, error) {
	f, err := readcommon.OpenNoFollowIn(root, rel)
	if err != nil {
		return nil, err //nolint:wrapcheck // keeps fs.ErrNotExist; the caller names the database
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, jq.NewErrf("%s is not a regular file", rel)
	}
	return f, nil
}

// readTable reads the credential table through the database and its log as
// they stand now.
func readTable(db, wal *os.File) ([]map[string]any, error) {
	info, err := db.Stat()
	if err != nil {
		return nil, jq.Sealed("stat the database", err)
	}
	var walSize int64
	if wal != nil {
		if walInfo, err := wal.Stat(); err == nil {
			walSize = walInfo.Size()
		}
	}
	d, err := sqliteread.Open(db, info.Size(), wal, walSize)
	if err != nil {
		return nil, err //nolint:wrapcheck // the reader's message is the warning's reason
	}
	return d.Table(loginTable) //nolint:wrapcheck // as above
}

// login is one integration's chosen row: its auth.json value, whether it is
// the active one, and when it changed.
type login struct {
	value   json.RawMessage
	active  bool
	updated int64
}

// logins picks each provider integration's login from the table's rows: a
// row switched away from (active 0) is skipped, the active one wins over
// an imported one, then the latest; an MCP server's login, a value that is
// not a JSON object, and a type auth.json does not hold are left out.
func logins(rows []map[string]any) map[string]json.RawMessage {
	chosen := map[string]login{}
	for _, row := range rows {
		id, next, ok := rowLogin(row)
		if prev, seen := chosen[id]; ok && (!seen || next.beats(prev)) {
			chosen[id] = next
		}
	}
	out := make(map[string]json.RawMessage, len(chosen))
	for id, l := range chosen {
		out[id] = l.value
	}
	return out
}

// rowLogin reads one row's provider login; false for a row left out.
func rowLogin(row map[string]any) (string, login, bool) {
	id, ok := row[loginColID].(string)
	if !ok || id == "" || strings.HasPrefix(id, mcpLoginPrefix) || row[loginColActive] == int64(0) {
		return "", login{}, false
	}
	value, ok := authValue(row[loginColValue])
	updated, _ := row[loginColUpdated].(int64) //nolint:errcheck // a missing time sorts first
	return id, login{value: value, active: row[loginColActive] == int64(1), updated: updated}, ok
}

// beats reports whether l replaces prev as its integration's login: the
// active one first, then the later.
func (l login) beats(prev login) bool {
	if l.active != prev.active {
		return l.active
	}
	return l.updated > prev.updated
}

// authValue is one row's value as auth.json holds it: the table's API key
// type rewritten as auth.json's, any other type auth.json holds kept as is.
func authValue(raw any) (json.RawMessage, bool) {
	text, ok := raw.(string)
	if !ok || len(text) > seedCap {
		return nil, false
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return nil, false
	}
	var kind string
	if err := json.Unmarshal(doc["type"], &kind); err != nil {
		return nil, false
	}
	if kind == loginTypeKey {
		return apiLogin(doc[loginTypeKey])
	}
	return json.RawMessage(text), slices.Contains(loginTypes, kind)
}

// apiLogin is the table's API key value as auth.json's "api" login.
func apiLogin(raw json.RawMessage) (json.RawMessage, bool) {
	var key string
	if err := json.Unmarshal(raw, &key); err != nil || key == "" {
		return nil, false
	}
	out, err := jq.Marshal(map[string]string{"type": loginTypeAPI, loginTypeKey: key})
	return out, err == nil
}

// writeLogins writes the logins over the seeded login file at path: each
// integration's entry replaced, every other entry of the copy kept.
func writeLogins(path string, logins map[string]json.RawMessage) error {
	doc := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &doc) //nolint:errcheck // a copy that is not an object is replaced whole
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	maps.Copy(doc, logins)
	out, err := jq.Marshal(doc)
	if err != nil {
		return jq.Sealed("encode the seeded logins", err)
	}
	if len(out) > seedCap {
		return jq.NewErrf("the seeded logins are larger than a login file")
	}
	if err := os.MkdirAll(filepath.Dir(path), viewDirMode); err != nil {
		return jq.Sealed("make a throwaway home", err)
	}
	// path is the catalog-cited login file joined under the launch's own TMPDIR.
	if err := os.WriteFile(path, out, seedMode); err != nil {
		return jq.Sealed("seed a throwaway home", err)
	}
	return nil
}
